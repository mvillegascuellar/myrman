package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/storage"
)

type Engine struct {
	Cfg   *config.Config
	DB    *catalog.DB
	Store *storage.Coordinator
	Phys  *catalog.PhysicalRepo
	Blog  *catalog.BinlogRepo
}

type Candidate struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // physical|binlog
	Reason string `json:"reason"`
	Tier   string `json:"tier"` // local|offsite
}

type Summary struct {
	Tagged     map[string]string `json:"tagged"`
	Candidates []Candidate       `json:"candidates"`
	Deleted    []string          `json:"deleted"`
}

func NewEngine(cfg *config.Config, db *catalog.DB, store *storage.Coordinator) *Engine {
	return &Engine{
		Cfg: cfg, DB: db, Store: store,
		Phys: catalog.NewPhysicalRepo(db),
		Blog: catalog.NewBinlogRepo(db),
	}
}

// Tag assigns GFS tags to completed physical backups.
func (e *Engine) Tag(ctx context.Context) (map[string]string, error) {
	backs, err := e.Phys.ListCompleted(ctx)
	if err != nil {
		return nil, err
	}
	// Sort ascending by end/start time
	sort.Slice(backs, func(i, j int) bool {
		return backupTime(backs[i]) < backupTime(backs[j])
	})

	type dayKey string
	firstOfDay := map[dayKey]*catalog.PhysicalBackup{}
	firstOfMonth := map[string]*catalog.PhysicalBackup{}
	firstOfYear := map[string]*catalog.PhysicalBackup{}

	for i := range backs {
		b := &backs[i]
		t := time.Unix(backupTime(*b), 0).UTC()
		dk := dayKey(t.Format("2006-01-02"))
		mk := t.Format("2006-01")
		yk := t.Format("2006")
		if _, ok := firstOfDay[dk]; !ok {
			firstOfDay[dk] = b
		}
		if t.Day() == 1 {
			if _, ok := firstOfMonth[mk]; !ok {
				firstOfMonth[mk] = b
			}
		}
		if t.Month() == time.January && t.Day() == 1 {
			if _, ok := firstOfYear[yk]; !ok {
				firstOfYear[yk] = b
			}
		}
	}

	// Reset tags then assign strongest
	tagged := map[string]string{}
	assign := map[string]catalog.GFSTag{}
	for _, b := range firstOfDay {
		assign[b.ID] = catalog.GFSDaily
	}
	for _, b := range firstOfMonth {
		assign[b.ID] = catalog.GFSMonthly
	}
	for _, b := range firstOfYear {
		assign[b.ID] = catalog.GFSYearly
	}

	for i := range backs {
		b := &backs[i]
		tag := catalog.GFSNone
		if t, ok := assign[b.ID]; ok {
			tag = t
		}
		if err := e.Phys.SetGFSTag(ctx, b.ID, tag); err != nil {
			return nil, err
		}
		tagged[b.ID] = string(tag)
		b.GFSTag = tag
	}
	return tagged, nil
}

// Run tags then computes prune candidates and optionally deletes.
func (e *Engine) Run(ctx context.Context, dryRun bool) (*Summary, error) {
	started := catalog.NowUnix()
	tagged, err := e.Tag(ctx)
	if err != nil {
		return nil, err
	}
	backs, err := e.Phys.ListCompleted(ctx)
	if err != nil {
		return nil, err
	}
	binlogs, err := e.Blog.List(ctx, 0)
	if err != nil {
		return nil, err
	}

	keepPhysical := e.computePhysicalKeep(backs)
	keepBinlog := e.computeBinlogKeep(backs, binlogs, keepPhysical)

	var cands []Candidate
	// Physical prune: only if not in keep and no kept descendant depends on it
	children := map[string][]string{}
	byID := map[string]catalog.PhysicalBackup{}
	for _, b := range backs {
		byID[b.ID] = b
		if b.ParentID.Valid {
			children[b.ParentID.String] = append(children[b.ParentID.String], b.ID)
		}
	}

	dependsOnKept := func(id string) bool {
		// BFS descendants; if any kept child exists, lock
		stack := []string{id}
		seen := map[string]bool{}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, c := range children[n] {
				if keepPhysical[c] {
					return true
				}
				if !seen[c] {
					seen[c] = true
					stack = append(stack, c)
				}
			}
		}
		return false
	}

	for _, b := range backs {
		if keepPhysical[b.ID] {
			continue
		}
		if dependsOnKept(b.ID) {
			continue // LSN dependency lock
		}
		cands = append(cands, Candidate{ID: b.ID, Kind: "physical", Reason: "outside retention window", Tier: "both"})
	}
	for _, a := range binlogs {
		if keepBinlog[a.ID] {
			continue
		}
		cands = append(cands, Candidate{ID: a.ID, Kind: "binlog", Reason: "outside binlog retention", Tier: "both"})
	}

	// Delete child-to-parent: sort physical candidates so children first
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Kind != cands[j].Kind {
			return cands[i].Kind == "physical" && cands[j].Kind != "physical"
		}
		if cands[i].Kind == "physical" {
			bi, bj := byID[cands[i].ID], byID[cands[j].ID]
			return bi.LSNTo > bj.LSNTo // higher LSN (children) first
		}
		return cands[i].ID < cands[j].ID
	})

	sum := &Summary{Tagged: tagged, Candidates: cands}
	if dryRun {
		_ = catalog.InsertRetentionRun(ctx, e.DB, true, mustJSON(sum), started, catalog.NowUnix())
		return sum, nil
	}

	for _, c := range cands {
		if c.Kind == "physical" {
			b := byID[c.ID]
			if err := e.deletePhysical(ctx, &b); err != nil {
				log.Printf("delete physical %s: %v", c.ID, err)
				continue
			}
			sum.Deleted = append(sum.Deleted, c.ID)
		} else {
			if err := e.deleteBinlog(ctx, c.ID, binlogs); err != nil {
				log.Printf("delete binlog %s: %v", c.ID, err)
				continue
			}
			sum.Deleted = append(sum.Deleted, c.ID)
		}
	}
	_ = catalog.InsertRetentionRun(ctx, e.DB, false, mustJSON(sum), started, catalog.NowUnix())
	return sum, nil
}

func (e *Engine) computePhysicalKeep(backs []catalog.PhysicalBackup) map[string]bool {
	keep := map[string]bool{}
	rc := e.Cfg.Retention

	var daily, monthly, yearly []catalog.PhysicalBackup
	for _, b := range backs {
		switch b.GFSTag {
		case catalog.GFSDaily:
			daily = append(daily, b)
		case catalog.GFSMonthly:
			monthly = append(monthly, b)
		case catalog.GFSYearly:
			yearly = append(yearly, b)
		}
	}
	sortByTimeDesc := func(s []catalog.PhysicalBackup) {
		sort.Slice(s, func(i, j int) bool { return backupTime(s[i]) > backupTime(s[j]) })
	}
	sortByTimeDesc(daily)
	sortByTimeDesc(monthly)
	sortByTimeDesc(yearly)

	take := func(s []catalog.PhysicalBackup, n int) {
		for i := 0; i < len(s) && i < n; i++ {
			keep[s[i].ID] = true
		}
	}
	take(daily, rc.OffsiteDaily)
	take(monthly, rc.OffsiteMonthly)
	take(yearly, rc.OffsiteYearly)

	// Always keep ancestors of kept backups (LSN chain)
	byID := map[string]catalog.PhysicalBackup{}
	for _, b := range backs {
		byID[b.ID] = b
	}
	for id := range keep {
		cur := byID[id]
		for cur.ParentID.Valid {
			pid := cur.ParentID.String
			keep[pid] = true
			cur = byID[pid]
			if cur.ID == "" {
				break
			}
		}
	}

	// Local: last N complete backup sets (FULL + descendant INCs)
	var fulls []catalog.PhysicalBackup
	for _, b := range backs {
		if b.BackupType == catalog.BackupFull {
			fulls = append(fulls, b)
		}
	}
	sortByTimeDesc(fulls)
	nSets := rc.LocalBackupSets
	if nSets < 1 {
		nSets = 2
	}
	children := map[string][]string{}
	for _, b := range backs {
		if b.ParentID.Valid {
			children[b.ParentID.String] = append(children[b.ParentID.String], b.ID)
		}
	}
	for i := 0; i < len(fulls) && i < nSets; i++ {
		keep[fulls[i].ID] = true
		stack := []string{fulls[i].ID}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, c := range children[n] {
				keep[c] = true
				stack = append(stack, c)
			}
		}
	}
	return keep
}

func (e *Engine) computeBinlogKeep(backs []catalog.PhysicalBackup, binlogs []catalog.BinlogArchive, keepPhysical map[string]bool) map[string]bool {
	keep := map[string]bool{}
	now := time.Now().UTC()
	offsiteCutoff := now.AddDate(0, 0, -e.Cfg.Retention.OffsiteBinlogDays).Unix()
	localCutoff := now.AddDate(0, 0, -e.Cfg.Retention.LocalBinlogDays).Unix()

	// Align offsite binlogs with oldest kept DAILY physical start if newer
	var oldestDaily int64
	for _, b := range backs {
		if !keepPhysical[b.ID] || b.GFSTag != catalog.GFSDaily {
			continue
		}
		t := backupTime(b)
		if oldestDaily == 0 || t < oldestDaily {
			oldestDaily = t
		}
	}
	if oldestDaily > offsiteCutoff {
		offsiteCutoff = oldestDaily
	}

	cutoff := offsiteCutoff
	if localCutoff < cutoff {
		cutoff = localCutoff
	}
	for _, a := range binlogs {
		if a.Status == string(catalog.StatusStreaming) {
			keep[a.ID] = true
			continue
		}
		t := a.EndTime.Int64
		if !a.EndTime.Valid {
			t = a.CreatedAt
		}
		if t >= cutoff {
			keep[a.ID] = true
		}
	}
	return keep
}

func (e *Engine) deletePhysical(ctx context.Context, b *catalog.PhysicalBackup) error {
	if b.LocalPath.Valid && b.LocalPath.String != "" {
		_ = os.Remove(b.LocalPath.String)
	}
	if b.CloudURL.Valid && e.Store.HasCloud() {
		_ = e.Store.Cloud.Delete(ctx, storage.KeyFromURL(b.CloudURL.String))
	}
	return e.Phys.MarkDeleted(ctx, b.ID)
}

func (e *Engine) deleteBinlog(ctx context.Context, id string, all []catalog.BinlogArchive) error {
	var a *catalog.BinlogArchive
	for i := range all {
		if all[i].ID == id {
			a = &all[i]
			break
		}
	}
	if a == nil {
		return fmt.Errorf("binlog %s not found", id)
	}
	if a.LocalPath.Valid {
		_ = os.Remove(a.LocalPath.String)
	}
	if a.CloudURL.Valid && e.Store.HasCloud() {
		_ = e.Store.Cloud.Delete(ctx, storage.KeyFromURL(a.CloudURL.String))
	}
	return e.Blog.MarkDeleted(ctx, id)
}

func backupTime(b catalog.PhysicalBackup) int64 {
	if b.EndTime.Valid {
		return b.EndTime.Int64
	}
	return b.StartTime
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

package recover

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/engine"
	"github.com/michaelvillegas/myrman/internal/executil"
	"github.com/michaelvillegas/myrman/internal/storage"
)

type Options struct {
	TargetTime time.Time
	TargetGTID string
	Apply      bool
	OutputDir  string
}

type Plan struct {
	Full       *catalog.PhysicalBackup
	Incrementals []catalog.PhysicalBackup
	Binlogs    []catalog.BinlogArchive
	PrepareCmds []string
	BinlogCmd  string
	ScriptPath string
}

type Orchestrator struct {
	Cfg   *config.Config
	DB    *catalog.DB
	Store *storage.Coordinator
	Phys  *catalog.PhysicalRepo
	Blog  *catalog.BinlogRepo
}

func New(cfg *config.Config, db *catalog.DB, store *storage.Coordinator) *Orchestrator {
	return &Orchestrator{
		Cfg: cfg, DB: db, Store: store,
		Phys: catalog.NewPhysicalRepo(db),
		Blog: catalog.NewBinlogRepo(db),
	}
}

func (o *Orchestrator) Recover(ctx context.Context, opts Options) (*Plan, error) {
	if opts.TargetTime.IsZero() && opts.TargetGTID == "" {
		return nil, fmt.Errorf("require --target-time or --target-gtid")
	}
	if !opts.TargetTime.IsZero() && opts.TargetGTID != "" {
		return nil, fmt.Errorf("--target-time and --target-gtid are mutually exclusive")
	}
	if err := o.Cfg.EnsureDirs(); err != nil {
		return nil, err
	}

	backs, err := o.Phys.ListCompleted(ctx)
	if err != nil {
		return nil, err
	}
	full, incs, err := selectChain(backs, opts)
	if err != nil {
		return nil, err
	}

	outDir := opts.OutputDir
	if outDir == "" {
		outDir = filepath.Join(o.Cfg.Local.Staging, "recover-"+full.ID[:8])
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, err
	}

	tool, err := engine.Resolve(o.Cfg.BackupTool)
	if err != nil {
		return nil, err
	}

	plan := &Plan{Full: full, Incrementals: incs}

	// Stage artifacts
	fullDir := filepath.Join(outDir, "full")
	if err := o.stageAndExtract(ctx, full, fullDir); err != nil {
		return nil, fmt.Errorf("stage full: %w", err)
	}
	incDirs := make([]string, len(incs))
	for i := range incs {
		incDirs[i] = filepath.Join(outDir, fmt.Sprintf("inc-%d", i))
		if err := o.stageAndExtract(ctx, &incs[i], incDirs[i]); err != nil {
			return nil, fmt.Errorf("stage incremental %s: %w", incs[i].ID, err)
		}
	}

	// Prepare chain
	for i := range incs {
		args := []string{"--prepare", "--apply-log-only", "--target-dir=" + fullDir, "--incremental-dir=" + incDirs[i]}
		cmd := tool + " " + strings.Join(args, " ")
		plan.PrepareCmds = append(plan.PrepareCmds, cmd)
		if opts.Apply {
			if err := executil.Run(ctx, tool, args...); err != nil {
				return plan, fmt.Errorf("prepare apply-log-only step %d: %w", i, err)
			}
		}
	}
	finalArgs := []string{"--prepare", "--target-dir=" + fullDir}
	finalCmd := tool + " " + strings.Join(finalArgs, " ")
	plan.PrepareCmds = append(plan.PrepareCmds, finalCmd)
	if opts.Apply {
		if err := executil.Run(ctx, tool, finalArgs...); err != nil {
			return plan, fmt.Errorf("final prepare: %w", err)
		}
	}

	// Binlogs for PITR
	fromSeq := int64(0)
	fromTime := full.EndTime.Int64
	if !full.EndTime.Valid {
		fromTime = full.StartTime
	}
	if full.BinlogFile.Valid {
		if seq, err := parseSeq(full.BinlogFile.String); err == nil {
			fromSeq = seq
		}
	}
	toTime := opts.TargetTime.Unix()
	if toTime == 0 {
		toTime = time.Now().UTC().Unix()
	}
	binlogs, err := o.Blog.ListInRange(ctx, fromSeq, fromTime, toTime)
	if err != nil {
		return plan, err
	}
	plan.Binlogs = binlogs

	var files []string
	for _, b := range binlogs {
		path := ""
		if b.LocalPath.Valid && fileExists(b.LocalPath.String) {
			path = b.LocalPath.String
		} else if b.CloudURL.Valid && o.Store.HasCloud() {
			dest := filepath.Join(outDir, "binlogs", b.Filename)
			if err := o.Store.DownloadTo(ctx, storage.KeyFromURL(b.CloudURL.String), dest); err != nil {
				log.Printf("warn: download binlog %s: %v", b.Filename, err)
				continue
			}
			path = dest
		}
		if path != "" {
			files = append(files, path)
		}
	}

	mblArgs := []string{}
	if !opts.TargetTime.IsZero() {
		mblArgs = append(mblArgs, "--stop-datetime="+opts.TargetTime.UTC().Format("2006-01-02 15:04:05"))
	}
	if opts.TargetGTID != "" {
		mblArgs = append(mblArgs, "--stop-position=1") // placeholder; GTID stop via --exclude-gtids inverse is complex
		// Prefer mysqlbinlog --gtid with stop via SQL until
		mblArgs = []string{}
	}
	if full.BinlogPos.Valid && full.BinlogFile.Valid {
		// start after backup position on first file matching
		_ = full
	}
	binlogCmd := "mysqlbinlog " + strings.Join(append(mblArgs, files...), " ")
	if opts.TargetGTID != "" {
		binlogCmd = fmt.Sprintf("mysqlbinlog %s | mysql --init-command=\"SET SESSION sql_log_bin=0\" -e \"/* stop before %s */\"",
			strings.Join(files, " "), opts.TargetGTID)
		// Practical MVP script:
		binlogCmd = fmt.Sprintf("mysqlbinlog %s | mysql -h%s -P%d -u%s",
			strings.Join(files, " "), o.Cfg.MySQL.Host, o.Cfg.MySQL.Port, o.Cfg.MySQL.User)
		binlogCmd += fmt.Sprintf("\n# Apply until GTID %s (stop manually or use gtids filtering)", opts.TargetGTID)
	} else if len(files) > 0 {
		binlogCmd = fmt.Sprintf("mysqlbinlog %s %s | mysql -h%s -P%d -u%s",
			strings.Join(mblArgs, " "), strings.Join(files, " "),
			o.Cfg.MySQL.Host, o.Cfg.MySQL.Port, o.Cfg.MySQL.User)
	} else {
		binlogCmd = "# no binlog archives found in catalog for PITR window"
	}
	plan.BinlogCmd = binlogCmd

	script := filepath.Join(outDir, "recover.sh")
	body := "#!/usr/bin/env bash\nset -euo pipefail\n\n# Prepared data dir: " + fullDir + "\n# Copy to datadir after review, then:\n\n"
	for _, c := range plan.PrepareCmds {
		body += "# " + c + "\n"
	}
	body += "\n" + plan.BinlogCmd + "\n"
	if err := os.WriteFile(script, []byte(body), 0o750); err != nil {
		return plan, err
	}
	plan.ScriptPath = script

	if opts.Apply && len(files) > 0 && !opts.TargetTime.IsZero() {
		args := append(mblArgs, files...)
		cmd := exec.CommandContext(ctx, "mysqlbinlog", args...)
		mysqlArgs := []string{
			fmt.Sprintf("-h%s", o.Cfg.MySQL.Host),
			fmt.Sprintf("-P%d", o.Cfg.MySQL.Port),
			"-u" + o.Cfg.MySQL.User,
		}
		if o.Cfg.MySQL.Password != "" {
			mysqlArgs = append(mysqlArgs, "-p"+o.Cfg.MySQL.Password)
		}
		mysql := exec.CommandContext(ctx, "mysql", mysqlArgs...)
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return plan, err
		}
		mysql.Stdin = pipe
		mysql.Stdout = os.Stdout
		mysql.Stderr = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return plan, err
		}
		if err := mysql.Start(); err != nil {
			_ = cmd.Process.Kill()
			return plan, err
		}
		_ = cmd.Wait()
		if err := mysql.Wait(); err != nil {
			return plan, fmt.Errorf("mysql apply: %w", err)
		}
	}

	log.Printf("recovery plan written to %s (prepared dir %s)", script, fullDir)
	return plan, nil
}

func (o *Orchestrator) stageAndExtract(ctx context.Context, b *catalog.PhysicalBackup, destDir string) error {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return err
	}
	src := ""
	if b.LocalPath.Valid && fileExists(b.LocalPath.String) {
		src = b.LocalPath.String
	} else if b.CloudURL.Valid && o.Store.HasCloud() {
		src = filepath.Join(o.Cfg.Local.Staging, "dl-"+b.ID+filepath.Ext(b.ArtifactName.String))
		if !fileExists(src) {
			if err := o.Store.DownloadTo(ctx, storage.KeyFromURL(b.CloudURL.String), src); err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("backup %s has no local or cloud artifact", b.ID)
	}

	streamPath := src
	name := src
	if b.ArtifactName.Valid {
		name = b.ArtifactName.String
	}
	if strings.HasSuffix(name, ".zst") || strings.HasSuffix(src, ".zst") ||
		strings.HasSuffix(name, ".gz") || strings.HasSuffix(src, ".gz") {
		tmp := filepath.Join(filepath.Dir(destDir), filepath.Base(src)+".raw")
		decomp := "zstd -d -c"
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(src, ".gz") {
			decomp = "gzip -d -c"
		}
		cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("%s %q > %q", decomp, src, tmp))
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("decompress: %w", err)
		}
		streamPath = tmp
		defer os.Remove(tmp)
	}

	if err := executil.LookOrError("xbstream"); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("xbstream -x -C %q < %q", destDir, streamPath))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// SelectChainForTest exports selectChain for unit tests.
func SelectChainForTest(backs []catalog.PhysicalBackup, opts Options) (*catalog.PhysicalBackup, []catalog.PhysicalBackup, error) {
	return selectChain(backs, opts)
}

func selectChain(backs []catalog.PhysicalBackup, opts Options) (*catalog.PhysicalBackup, []catalog.PhysicalBackup, error) {
	byID := map[string]catalog.PhysicalBackup{}
	children := map[string][]catalog.PhysicalBackup{}
	for _, b := range backs {
		byID[b.ID] = b
		if b.ParentID.Valid {
			children[b.ParentID.String] = append(children[b.ParentID.String], b)
		}
	}

	targetUnix := opts.TargetTime.Unix()
	if opts.TargetTime.IsZero() {
		targetUnix = time.Now().UTC().Unix()
	}

	var best *catalog.PhysicalBackup
	for i := range backs {
		b := &backs[i]
		if b.BackupType != catalog.BackupFull {
			continue
		}
		t := b.EndTime.Int64
		if !b.EndTime.Valid {
			t = b.StartTime
		}
		if t > targetUnix {
			continue
		}
		// GTID heuristic: if target gtid set, prefer backups whose gtid_executed is non-empty and earlier
		if opts.TargetGTID != "" && b.GTIDExecuted.Valid {
			if strings.Contains(b.GTIDExecuted.String, strings.Split(opts.TargetGTID, ":")[0]) {
				// still use time/lsn ranking
			}
		}
		if best == nil || b.LSNTo > best.LSNTo {
			cp := *b
			best = &cp
		}
	}
	if best == nil {
		return nil, nil, fmt.Errorf("no FULL backup preceding recovery target")
	}

	// Walk incremental chain maximizing LSN while end_time <= target
	var chain []catalog.PhysicalBackup
	cur := best
	for {
		cands := children[cur.ID]
		var next *catalog.PhysicalBackup
		for i := range cands {
			c := &cands[i]
			if c.BackupType != catalog.BackupIncremental {
				continue
			}
			if err := catalog.ValidateLSNContinuity(cur.LSNTo, c.LSNFrom); err != nil {
				continue
			}
			t := c.EndTime.Int64
			if !c.EndTime.Valid {
				t = c.StartTime
			}
			if t > targetUnix {
				continue
			}
			if next == nil || c.LSNTo > next.LSNTo {
				cp := *c
				next = &cp
			}
		}
		if next == nil {
			break
		}
		chain = append(chain, *next)
		cur = next
	}
	return best, chain, nil
}

func parseSeq(name string) (int64, error) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return 0, fmt.Errorf("no seq")
	}
	var n int64
	_, err := fmt.Sscanf(name[i+1:], "%d", &n)
	return n, err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

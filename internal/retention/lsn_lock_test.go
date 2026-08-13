package retention_test

import (
	"testing"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/retention"
	"github.com/michaelvillegas/myrman/internal/storage"
)

// TestLSNDependencyLock ensures a FULL is never listed for deletion while a
// surviving INCREMENTAL still depends on its to_lsn / parent_id.
func TestLSNDependencyLock(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Local.Root = dir
	cfg.Local.Staging = dir + "/staging"
	cfg.Local.PhysicalDir = dir + "/physical"
	cfg.Local.BinlogDir = dir + "/binlogs"
	cfg.Catalog = dir + "/catalog.db"
	// Aggressive offsite windows so older tags fall out, but local sets keep latest chain.
	cfg.Retention.OffsiteDaily = 0
	cfg.Retention.OffsiteMonthly = 0
	cfg.Retention.OffsiteYearly = 0
	cfg.Retention.LocalBackupSets = 1
	cfg.Cloud.Provider = "none"
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	db, err := catalog.Open(cfg.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := storage.NewCoordinator(cfg)
	repo := catalog.NewPhysicalRepo(db)

	// Latest set: FULL + INC (must be kept by local_backup_sets=1)
	now := time.Now().UTC().Unix()
	full := insertFull(t, repo, "full-latest", 0, 1000, now)
	inc := insertInc(t, repo, "inc-latest", full.ID, 1000, 2000, now+10)

	// Orphan old full with no children — should be prune candidate
	old := insertFull(t, repo, "full-ancient", 0, 10, now-86400*400)

	sum, err := retention.NewEngine(cfg, db, store).Run(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	cands := map[string]bool{}
	for _, c := range sum.Candidates {
		cands[c.ID] = true
	}
	if cands[full.ID] {
		t.Fatalf("FULL %s must not be prune candidate while INC %s depends on it", full.ID, inc.ID)
	}
	if cands[inc.ID] {
		t.Fatalf("INC %s is part of latest local set and must be kept", inc.ID)
	}
	if !cands[old.ID] {
		t.Fatalf("ancient FULL %s should be a prune candidate; tags=%v", old.ID, sum.Tagged)
	}
}

package recover_test

import (
	"testing"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/recover"
)

func TestSelectChain(t *testing.T) {
	t1 := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 3, 1, 14, 0, 0, 0, time.UTC)
	backs := []catalog.PhysicalBackup{
		{
			ID: "f1", BackupType: catalog.BackupFull, LSNFrom: 0, LSNTo: 100,
			StartTime: t1.Unix(), EndTime: catalog.NullInt64(t1.Unix(), true), Status: catalog.StatusCompleted,
		},
		{
			ID: "i1", ParentID: catalog.NullString("f1"), BackupType: catalog.BackupIncremental,
			LSNFrom: 100, LSNTo: 200,
			StartTime: t2.Unix(), EndTime: catalog.NullInt64(t2.Unix(), true), Status: catalog.StatusCompleted,
		},
		{
			ID: "i2", ParentID: catalog.NullString("i1"), BackupType: catalog.BackupIncremental,
			LSNFrom: 200, LSNTo: 300,
			StartTime: t3.Unix(), EndTime: catalog.NullInt64(t3.Unix(), true), Status: catalog.StatusCompleted,
		},
	}
	target := time.Date(2024, 3, 1, 13, 0, 0, 0, time.UTC)
	full, incs, err := recover.SelectChainForTest(backs, recover.Options{TargetTime: target})
	if err != nil {
		t.Fatal(err)
	}
	if full.ID != "f1" {
		t.Fatalf("full=%s", full.ID)
	}
	if len(incs) != 1 || incs[0].ID != "i1" {
		t.Fatalf("incs=%v", incs)
	}
}

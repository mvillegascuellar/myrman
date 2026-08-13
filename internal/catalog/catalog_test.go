package catalog_test

import (
	"testing"

	"github.com/michaelvillegas/myrman/internal/catalog"
)

func TestValidateLSNContinuity(t *testing.T) {
	if err := catalog.ValidateLSNContinuity(100, 100); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateLSNContinuity(100, 101); err == nil {
		t.Fatal("expected error")
	}
}

func TestMigrateAndCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := catalog.NewPhysicalRepo(db)
	b := &catalog.PhysicalBackup{
		ID:              "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		BackupType:      catalog.BackupFull,
		BackupTool:      "xtrabackup",
		LSNFrom:          0,
		LSNTo:            1000,
		StartTime:       1700000000,
		EndTime:         catalog.NullInt64(1700000100, true),
		GFSTag:          catalog.GFSNone,
		StorageLocation: catalog.StorageLocal,
		Status:          catalog.StatusCompleted,
		CreatedAt:       1700000000,
	}
	if err := repo.Insert(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LSNTo != 1000 {
		t.Fatalf("%d", got.LSNTo)
	}

	inc := &catalog.PhysicalBackup{
		ID:              "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee",
		ParentID:        catalog.NullString(b.ID),
		BackupType:      catalog.BackupIncremental,
		BackupTool:      "xtrabackup",
		LSNFrom:          1000,
		LSNTo:            2000,
		StartTime:       1700000200,
		EndTime:         catalog.NullInt64(1700000300, true),
		GFSTag:          catalog.GFSNone,
		StorageLocation: catalog.StorageLocal,
		Status:          catalog.StatusCompleted,
		CreatedAt:       1700000200,
	}
	if err := catalog.ValidateLSNContinuity(b.LSNTo, inc.LSNFrom); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(t.Context(), inc); err != nil {
		t.Fatal(err)
	}
	head, err := repo.LatestChainHead(t.Context())
	if err != nil || head == nil || head.ID != inc.ID {
		t.Fatalf("head=%v err=%v", head, err)
	}
}

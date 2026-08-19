package engine

import (
	"testing"
)

func TestSupportsFlag(t *testing.T) {
	helpCache.Store("fake-xb", "Usage:\n  --backup\n  --binlog-info=name\n  --no-server-version-check\n")
	if !SupportsFlag("fake-xb", "binlog-info") {
		t.Fatal("expected binlog-info")
	}
	if SupportsFlag("fake-xb", "not-a-real-flag") {
		t.Fatal("unexpected flag")
	}
	flags := BackupFlags("fake-xb")
	found := false
	for _, a := range flags {
		if a == "--binlog-info=ON" {
			found = true
		}
	}
	if !found {
		t.Fatalf("flags=%v", flags)
	}

	helpCache.Store("xb80", "Usage:\n  --backup\n  --no-server-version-check\n")
	for _, a := range BackupFlags("xb80") {
		if a == "--binlog-info=ON" {
			t.Fatal("8.0-style help should not get --binlog-info")
		}
	}
}

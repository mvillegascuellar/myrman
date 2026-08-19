package main

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
)

func TestFormatUnixUTC(t *testing.T) {
	if formatUnixUTC(0) != "-" {
		t.Fatal(formatUnixUTC(0))
	}
	got := formatUnixUTC(1787180525)
	if got != time.Unix(1787180525, 0).UTC().Format("2006-01-02 15:04:05") {
		t.Fatal(got)
	}
}

func TestFormatDuration(t *testing.T) {
	end := sql.NullInt64{Int64: 1004, Valid: true}
	if got := formatDuration(1000, end); got != "4s" {
		t.Fatalf("got %s", got)
	}
	if got := formatDuration(1000, sql.NullInt64{}); got != "-" {
		t.Fatalf("got %s", got)
	}
	if got := compactDuration(3661 * time.Second); got != "1h01m01s" {
		t.Fatalf("got %s", got)
	}
}

func TestPrintPhysicalTable(t *testing.T) {
	var buf bytes.Buffer
	rows := []catalog.PhysicalBackup{
		{
			ID:         "fa1ca110-a42c-4ad9-8492-8945333babcc",
			BackupType: catalog.BackupFull,
			StartTime:  1787180525,
			EndTime:    sql.NullInt64{Int64: 1787180529, Valid: true},
			Status:     catalog.StatusCompleted,
		},
		{
			ID:         "a5ff2956-79fd-435e-a82d-c2ad8f99c269",
			BackupType: catalog.BackupFull,
			StartTime:  1787180017,
			EndTime:    sql.NullInt64{Int64: 1787180017, Valid: true},
			Status:     catalog.StatusFailed,
		},
	}
	if err := printPhysicalTable(&buf, rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "COMPLETED") || !strings.Contains(out, "FAILED") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "FULL") {
		t.Fatal(out)
	}
}

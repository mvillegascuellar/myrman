package binlog

import "testing"

func TestChooseStartLogFirstAvailable(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013", "binlog.000014"}
	got, err := ChooseStartLog(StartHint{}, logs)
	if err != nil || got != "binlog.000012" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestChooseStartLogRequested(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013"}
	got, err := ChooseStartLog(StartHint{Requested: "binlog.000013"}, logs)
	if err != nil || got != "binlog.000013" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := ChooseStartLog(StartHint{Requested: "mysql-bin.000001"}, logs); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestChooseStartLogAfterCataloged(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013", "binlog.000014"}
	got, err := ChooseStartLog(StartHint{LastCataloged: "binlog.000012"}, logs)
	if err != nil || got != "binlog.000013" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = ChooseStartLog(StartHint{LastCataloged: "binlog.000014"}, logs)
	if err != nil || got != "binlog.000014" {
		t.Fatalf("resume current last got %q err=%v", got, err)
	}
}

func TestChooseStartLogPurgedCataloged(t *testing.T) {
	logs := []string{"binlog.000020", "binlog.000021"}
	got, err := ChooseStartLog(StartHint{LastCataloged: "binlog.000010"}, logs)
	if err != nil || got != "binlog.000020" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestChooseStartLogBackupBinlog(t *testing.T) {
	logs := []string{"binlog.000001", "binlog.000008", "binlog.000009"}
	got, err := ChooseStartLog(StartHint{BackupBinlog: "binlog.000008"}, logs)
	if err != nil || got != "binlog.000008" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestLogsFrom(t *testing.T) {
	logs := []string{"a", "b", "c"}
	got := logsFrom(logs, "b")
	if len(got) != 2 || got[0] != "b" {
		t.Fatalf("%v", got)
	}
}

func TestShouldCatalogBinlog(t *testing.T) {
	if shouldCatalogBinlog("binlog.000001", 10) {
		t.Fatal("should skip leftover files before dump start")
	}
	if !shouldCatalogBinlog("binlog.000010", 10) {
		t.Fatal("should catalog dump start file")
	}
	if shouldCatalogBinlog("notes.txt", 1) {
		t.Fatal("should skip non-binlog names")
	}
}

func TestIsAnonymousGTIDDumpError(t *testing.T) {
	msg := "Cannot replicate anonymous transaction when @@GLOBAL.GTID_MODE = ON, at file ./binlog.000001, position 157."
	if !isAnonymousGTIDDumpError(msg) {
		t.Fatal("expected match")
	}
	if isAnonymousGTIDDumpError("access denied") {
		t.Fatal("unexpected match")
	}
}

func TestParseShowBinaryLogs(t *testing.T) {
	out := "binlog.000003\t157\tNo\nbinlog.000004\t4096\tNo\n"
	got := parseShowBinaryLogs(out)
	if len(got) != 2 || got[0] != "binlog.000003" || got[1] != "binlog.000004" {
		t.Fatalf("%v", got)
	}
}

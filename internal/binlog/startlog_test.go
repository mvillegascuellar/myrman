package binlog

import "testing"

func TestChooseStartLogFirstAvailable(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013", "binlog.000014"}
	got, err := ChooseStartLog("", "", logs)
	if err != nil || got != "binlog.000012" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestChooseStartLogRequested(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013"}
	got, err := ChooseStartLog("binlog.000013", "", logs)
	if err != nil || got != "binlog.000013" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := ChooseStartLog("mysql-bin.000001", "", logs); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestChooseStartLogAfterCataloged(t *testing.T) {
	logs := []string{"binlog.000012", "binlog.000013", "binlog.000014"}
	got, err := ChooseStartLog("", "binlog.000012", logs)
	if err != nil || got != "binlog.000013" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = ChooseStartLog("", "binlog.000014", logs)
	if err != nil || got != "binlog.000014" {
		t.Fatalf("resume current last got %q err=%v", got, err)
	}
}

func TestChooseStartLogPurgedCataloged(t *testing.T) {
	logs := []string{"binlog.000020", "binlog.000021"}
	got, err := ChooseStartLog("", "binlog.000010", logs)
	if err != nil || got != "binlog.000020" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestParseShowBinaryLogs(t *testing.T) {
	out := "binlog.000003\t157\tNo\nbinlog.000004\t4096\tNo\n"
	got := parseShowBinaryLogs(out)
	if len(got) != 2 || got[0] != "binlog.000003" || got[1] != "binlog.000004" {
		t.Fatalf("%v", got)
	}
}

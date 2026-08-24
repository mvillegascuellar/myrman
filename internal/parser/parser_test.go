package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaelvillegas/myrman/internal/parser"
)

func TestParseCheckpointsFull(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "checkpoints_full.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp, err := parser.ParseCheckpoints(f)
	if err != nil {
		t.Fatal(err)
	}
	if cp.BackupType != "full-backuped" {
		t.Fatalf("type=%s", cp.BackupType)
	}
	if cp.FromLSN != 0 || cp.ToLSN != 123456789 {
		t.Fatalf("lsn %d..%d", cp.FromLSN, cp.ToLSN)
	}
	if parser.CatalogBackupType(cp.BackupType) != "FULL" {
		t.Fatal("catalog type")
	}
}

func TestParseCheckpointsIncremental(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "checkpoints_inc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp, err := parser.ParseCheckpoints(f)
	if err != nil {
		t.Fatal(err)
	}
	if cp.FromLSN != 123456789 || cp.ToLSN != 223456789 {
		t.Fatalf("lsn %d..%d", cp.FromLSN, cp.ToLSN)
	}
	if parser.CatalogBackupType(cp.BackupType) != "INCREMENTAL" {
		t.Fatal("catalog type")
	}
}

func TestParseCheckpointsLastLSNFallback(t *testing.T) {
	r := strings.NewReader("backup_type = full-backuped\nfrom_lsn = 1\nlast_lsn = 99\n")
	cp, err := parser.ParseCheckpoints(r)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ToLSN != 99 {
		t.Fatalf("expected last_lsn fallback, got %d", cp.ToLSN)
	}
}

func TestParseInfo(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "xtrabackup_info.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := parser.ParseInfo(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerUUID != "3e11fa47-71ca-11e1-9e33-c80aa9429562" {
		t.Fatalf("uuid=%s", info.ServerUUID)
	}
	if info.BinlogFile != "mysql-bin.000042" || info.BinlogPos != 98456321 {
		t.Fatalf("binlog %s:%d", info.BinlogFile, info.BinlogPos)
	}
	if !strings.Contains(info.GTIDExecuted, "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5000") {
		t.Fatalf("gtid=%s", info.GTIDExecuted)
	}
}

func TestParseInfoPXB84GTID(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "xtrabackup_info_pxb84.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := parser.ParseInfo(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.BinlogFile != "mysql-bin.000003" || info.BinlogPos != 157 {
		t.Fatalf("binlog %s:%d", info.BinlogFile, info.BinlogPos)
	}
	if info.GTIDExecuted != "1a34264e-9ac0-11f1-9e76-5254005f988f:1-61" {
		t.Fatalf("gtid=%s", info.GTIDExecuted)
	}
}

func TestParseBinlogInfo(t *testing.T) {
	r := strings.NewReader("mysql-bin.000002\t1232\tc777888a-b6df-11e2-a604-080027635ef5:1-4\n")
	info, err := parser.ParseBinlogInfo(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.BinlogFile != "mysql-bin.000002" || info.BinlogPos != 1232 {
		t.Fatalf("%s:%d", info.BinlogFile, info.BinlogPos)
	}
	if info.GTIDExecuted != "c777888a-b6df-11e2-a604-080027635ef5:1-4" {
		t.Fatalf("gtid=%s", info.GTIDExecuted)
	}
}

func TestSequenceFromFilename(t *testing.T) {
	n, err := parser.SequenceFromFilename("mysql-bin.000123")
	if err != nil || n != 123 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestParseBinlogOutputPreviousGTIDs(t *testing.T) {
	sample := `#260819 23:39:25 server id 1  end_log_pos 126 CRC32 0xf6936257 	Start: binlog v 4, server v 8.0.46-37 created 260819 23:39:25
# at 126
#260819 23:39:25 server id 1  end_log_pos 197 CRC32 0x39b97859 	Previous-GTIDs
# 1a34264e-9ac0-11f1-9e76-5254005f988f:1-43691
# at 197
SET @@SESSION.GTID_NEXT= '1a34264e-9ac0-11f1-9e76-5254005f988f:43692'/*!*/;
`
	b, err := parser.ParseBinlogBoundsForTest("binlog.000010", sample)
	if err != nil {
		t.Fatal(err)
	}
	if b.StartGTID != "1a34264e-9ac0-11f1-9e76-5254005f988f:1-43691" {
		t.Fatalf("start gtid=%s", b.StartGTID)
	}
	if b.EndGTID != "1a34264e-9ac0-11f1-9e76-5254005f988f:43692" {
		t.Fatalf("end gtid=%s", b.EndGTID)
	}
}

func TestParseBinlogOutputEmptyPreviousGTIDs(t *testing.T) {
	sample := `#260819 23:18:02 server id 1  end_log_pos 126
#260819 23:18:02 server id 1  end_log_pos 157 	Previous-GTIDs
# [empty]
SET @@SESSION.GTID_NEXT= '1a34264e-9ac0-11f1-9e76-5254005f988f:1'/*!*/;
`
	b, err := parser.ParseBinlogBoundsForTest("binlog.000006", sample)
	if err != nil {
		t.Fatal(err)
	}
	if b.StartGTID != "1a34264e-9ac0-11f1-9e76-5254005f988f:1" {
		t.Fatalf("start gtid=%s", b.StartGTID)
	}
}

func TestParseBinlogOutput(t *testing.T) {
	sample := `#240115 10:00:01 server id 1  end_log_pos 123 CRC32 ...
# GTID_NEXT= 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee:1'
#240115 10:05:00 server id 1  end_log_pos 456
SET @@SESSION.GTID_NEXT= 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee:99'
`
	// use unexported via ParseBinlogBounds would need mysqlbinlog; test helper path through exported Sequence
	_ = sample
	b, err := parser.ParseBinlogBoundsForTest("mysql-bin.000010", sample)
	if err != nil {
		t.Fatal(err)
	}
	if b.Sequence != 10 {
		t.Fatalf("seq=%d", b.Sequence)
	}
	if b.StartGTID == "" || b.EndGTID == "" {
		t.Fatalf("gtids start=%s end=%s", b.StartGTID, b.EndGTID)
	}
	if b.StartTime.IsZero() || b.EndTime.IsZero() {
		t.Fatal("times missing")
	}
}

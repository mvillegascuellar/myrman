package parser

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BinlogBounds extracted from a raw binlog file via mysqlbinlog.
type BinlogBounds struct {
	StartTime time.Time
	EndTime   time.Time
	StartGTID string
	EndGTID   string
	Filename  string
	Sequence  int64
}

var (
	tsRe   = regexp.MustCompile(`#(\d{6})\s+(\d{1,2}:\d{2}:\d{2})`)
	gtidRe = regexp.MustCompile(`(?i)GTID[_\s]?[^=]*=\s*([0-9a-fA-F-]{36}:\d+(?:-\d+)?)`)
	seqRe  = regexp.MustCompile(`\.(\d+)$`)
)

// ParseBinlogBounds runs mysqlbinlog on path and extracts first/last timestamps and GTIDs.
func ParseBinlogBounds(path string) (*BinlogBounds, error) {
	cmd := exec.Command("mysqlbinlog", "--base64-output=DECODE-ROWS", "-v", path)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("mysqlbinlog %s: %w: %s", path, err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("mysqlbinlog %s: %w", path, err)
	}
	return parseBinlogOutput(path, string(out))
}

// ParseBinlogBoundsForTest exposes parseBinlogOutput for unit tests.
func ParseBinlogBoundsForTest(path, text string) (*BinlogBounds, error) {
	return parseBinlogOutput(path, text)
}

func parseBinlogOutput(path, text string) (*BinlogBounds, error) {
	b := &BinlogBounds{Filename: baseName(path)}
	if m := seqRe.FindStringSubmatch(b.Filename); len(m) == 2 {
		b.Sequence, _ = strconv.ParseInt(m[1], 10, 64)
	}

	var times []time.Time
	var gtids []string
	sc := bufio.NewScanner(strings.NewReader(text))
	// allow long lines
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := tsRe.FindStringSubmatch(line); len(m) == 3 {
			if t, err := parseBinlogTS(m[1], m[2]); err == nil {
				times = append(times, t)
			}
		}
		if m := gtidRe.FindStringSubmatch(line); len(m) == 2 {
			gtids = append(gtids, m[1])
		}
		// SET @@SESSION.GTID_NEXT= 'uuid:n'
		if strings.Contains(line, "GTID_NEXT") {
			re := regexp.MustCompile(`'([0-9a-fA-F-]{36}:\d+)'`)
			if m := re.FindStringSubmatch(line); len(m) == 2 {
				gtids = append(gtids, m[1])
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(times) > 0 {
		b.StartTime = times[0]
		b.EndTime = times[len(times)-1]
	}
	if len(gtids) > 0 {
		b.StartGTID = gtids[0]
		b.EndGTID = gtids[len(gtids)-1]
	}
	return b, nil
}

func parseBinlogTS(ymd, hms string) (time.Time, error) {
	// yymmdd HH:MM:SS — assume 2000+ for yy
	layout := "060102 15:04:05"
	return time.ParseInLocation(layout, ymd+" "+hms, time.UTC)
}

func baseName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// SequenceFromFilename extracts numeric suffix from mysql-bin.000123.
func SequenceFromFilename(name string) (int64, error) {
	m := seqRe.FindStringSubmatch(name)
	if len(m) != 2 {
		return 0, fmt.Errorf("no sequence in %q", name)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

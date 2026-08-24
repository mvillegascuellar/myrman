package parser

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BinlogBounds extracted from a raw binlog file via mysqlbinlog.
type BinlogBounds struct {
	StartTime     time.Time
	EndTime       time.Time
	PreviousGTIDs string // GTID set executed before this file (Previous-GTIDs event)
	StartGTID     string // same as PreviousGTIDs; empty when Previous-GTIDs is [empty]
	EndGTID       string // GTID set contained in this file (merged GTID_NEXT values)
	Filename      string
	Sequence      int64
}

var (
	tsRe          = regexp.MustCompile(`#(\d{6})\s+(\d{1,2}:\d{2}:\d{2})`)
	gtidNextRe    = regexp.MustCompile(`(?i)GTID_NEXT\s*=\s*'([0-9a-fA-F-]{36}:\d+)'`)
	gtidSetRe     = regexp.MustCompile(`(?i)([0-9a-fA-F-]{36}:\d+(?:-\d+)?)`)
	seqRe         = regexp.MustCompile(`\.(\d+)$`)
	prevGTIDsMark = regexp.MustCompile(`(?i)Previous-GTIDs`)
)

// ParseBinlogBounds runs mysqlbinlog on path and extracts first/last timestamps and GTIDs.
func ParseBinlogBounds(path string) (*BinlogBounds, error) {
	return parseBinlogFile(path, 0)
}

// ParseBinlogHeader only reads the start of the file (Previous-GTIDs / first GTID).
func ParseBinlogHeader(path string) (*BinlogBounds, error) {
	return parseBinlogFile(path, 65536)
}

func parseBinlogFile(path string, stopPos int) (*BinlogBounds, error) {
	args := []string{"--base64-output=DECODE-ROWS", "-v"}
	if stopPos > 0 {
		args = append(args, fmt.Sprintf("--stop-position=%d", stopPos))
	}
	args = append(args, path)
	cmd := exec.Command("mysqlbinlog", args...)
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
	var prevGTIDs string
	inPrevGTIDs := false
	sc := bufio.NewScanner(strings.NewReader(text))
	// allow long lines
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if prevGTIDsMark.MatchString(line) {
			inPrevGTIDs = true
			continue
		}
		if inPrevGTIDs {
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			inPrevGTIDs = false
			if trimmed != "" && !strings.EqualFold(trimmed, "[empty]") {
				if gtidSetRe.MatchString(trimmed) {
					prevGTIDs = trimmed
				}
			}
		}
		if m := tsRe.FindStringSubmatch(line); len(m) == 3 {
			if t, err := parseBinlogTS(m[1], m[2]); err == nil {
				times = append(times, t)
			}
		}
		if m := gtidNextRe.FindStringSubmatch(line); len(m) == 2 {
			if !strings.EqualFold(m[1], "AUTOMATIC") {
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
	b.PreviousGTIDs = prevGTIDs
	b.StartGTID = prevGTIDs
	b.EndGTID = CompactGTIDSet(gtids)
	return b, nil
}

// CompactGTIDSet merges uuid:n values into MySQL GTID-set form (uuid:1-61).
func CompactGTIDSet(ids []string) string {
	type pair struct {
		uuid string
		n    int64
	}
	var items []pair
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || strings.EqualFold(id, "AUTOMATIC") {
			continue
		}
		uuid, n, ok := splitSingleGTID(id)
		if !ok {
			continue
		}
		key := uuid + ":" + strconv.FormatInt(n, 10)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, pair{uuid, n})
	}
	if len(items) == 0 {
		return ""
	}
	byUUID := map[string][]int64{}
	var uuids []string
	for _, it := range items {
		if _, ok := byUUID[it.uuid]; !ok {
			uuids = append(uuids, it.uuid)
		}
		byUUID[it.uuid] = append(byUUID[it.uuid], it.n)
	}
	var parts []string
	for _, uuid := range uuids {
		ns := byUUID[uuid]
		sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
		var ranges []string
		lo, hi := ns[0], ns[0]
		for _, n := range ns[1:] {
			if n == hi || n == hi+1 {
				if n > hi {
					hi = n
				}
				continue
			}
			ranges = append(ranges, formatGTIDRange(lo, hi))
			lo, hi = n, n
		}
		ranges = append(ranges, formatGTIDRange(lo, hi))
		parts = append(parts, uuid+":"+strings.Join(ranges, ":"))
	}
	return strings.Join(parts, ",")
}

func splitSingleGTID(id string) (uuid string, n int64, ok bool) {
	i := strings.LastIndex(id, ":")
	if i <= 0 || i == len(id)-1 {
		return "", 0, false
	}
	if strings.Contains(id[i+1:], "-") {
		return "", 0, false
	}
	n, err := strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

func formatGTIDRange(lo, hi int64) string {
	if lo == hi {
		return strconv.FormatInt(lo, 10)
	}
	return strconv.FormatInt(lo, 10) + "-" + strconv.FormatInt(hi, 10)
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

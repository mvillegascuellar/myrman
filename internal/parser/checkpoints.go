package parser

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Checkpoints holds fields from xtrabackup_checkpoints.
type Checkpoints struct {
	BackupType string // full-backuped | log-applied | incremental
	FromLSN    int64
	ToLSN      int64
	LastLSN    int64
}

// Info holds fields from xtrabackup_info.
type Info struct {
	GTIDExecuted string
	BinlogFile   string
	BinlogPos    int64
	ServerUUID   string
	ToolVersion  string
	StartTime    string
	EndTime      string
}

var kvLine = regexp.MustCompile(`^\s*([A-Za-z0-9_]+)\s*=\s*(.*?)\s*$`)

func ParseCheckpoints(r io.Reader) (*Checkpoints, error) {
	vals := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := kvLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		vals[strings.ToLower(m[1])] = m[2]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	cp := &Checkpoints{BackupType: vals["backup_type"]}
	var err error
	if cp.FromLSN, err = parseLSN(vals["from_lsn"]); err != nil {
		return nil, fmt.Errorf("from_lsn: %w", err)
	}
	if v, ok := vals["to_lsn"]; ok && v != "" {
		if cp.ToLSN, err = parseLSN(v); err != nil {
			return nil, fmt.Errorf("to_lsn: %w", err)
		}
	}
	if v, ok := vals["last_lsn"]; ok && v != "" {
		if cp.LastLSN, err = parseLSN(v); err != nil {
			return nil, fmt.Errorf("last_lsn: %w", err)
		}
	}
	if cp.ToLSN == 0 && cp.LastLSN != 0 {
		cp.ToLSN = cp.LastLSN
	}
	if cp.BackupType == "" {
		return nil, fmt.Errorf("missing backup_type")
	}
	if cp.ToLSN == 0 {
		return nil, fmt.Errorf("missing to_lsn/last_lsn")
	}
	return cp, nil
}

func parseLSN(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// ParseInfo extracts GTID, binlog coordinates, and server UUID from xtrabackup_info.
func ParseInfo(r io.Reader) (*Info, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	info := &Info{}

	// Flat key=value lines
	sc := bufio.NewScanner(strings.NewReader(text))
	vals := map[string]string{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		m := kvLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		vals[strings.ToLower(m[1])] = strings.Trim(m[2], `"'`)
	}

	info.GTIDExecuted = firstNonEmpty(vals["gtid_executed"], vals["gtid_purged"])
	info.ServerUUID = firstNonEmpty(vals["uuid"], vals["server_uuid"], vals["serverid"])
	info.ToolVersion = firstNonEmpty(vals["tool_version"], vals["xtrabackup_version"])
	info.StartTime = vals["start_time"]
	info.EndTime = vals["end_time"]

	// binlog_pos may be "filename\tposition" or separate keys
	if bp := vals["binlog_pos"]; bp != "" {
		file, pos, ok := splitBinlogPos(bp)
		if ok {
			info.BinlogFile = file
			info.BinlogPos = pos
		}
	}
	if info.BinlogFile == "" {
		info.BinlogFile = firstNonEmpty(vals["binlog"], vals["binlog_file"], vals["filename"])
	}
	if info.BinlogPos == 0 {
		if p := firstNonEmpty(vals["position"], vals["pos"], vals["binlog_position"]); p != "" {
			info.BinlogPos, _ = strconv.ParseInt(p, 10, 64)
		}
	}

	// Also scan multi-line GTID blocks: gtid_executed = UUID:1-10,\nUUID2:1-5
	if info.GTIDExecuted == "" {
		info.GTIDExecuted = extractMultilineGTID(text)
	}
	if info.ServerUUID == "" {
		re := regexp.MustCompile(`(?i)server[_ ]?uuid\s*[=:]\s*([0-9a-f-]{36})`)
		if m := re.FindStringSubmatch(text); len(m) == 2 {
			info.ServerUUID = m[1]
		}
	}

	return info, nil
}

func splitBinlogPos(s string) (string, int64, bool) {
	s = strings.TrimSpace(s)
	// formats: "mysql-bin.000001\t123" or "mysql-bin.000001:123" or "file pos"
	for _, sep := range []string{"\t", ":", " "} {
		if i := strings.LastIndex(s, sep); i > 0 {
			file := strings.TrimSpace(s[:i])
			posStr := strings.TrimSpace(s[i+len(sep):])
			pos, err := strconv.ParseInt(posStr, 10, 64)
			if err == nil && file != "" {
				return file, pos, true
			}
		}
	}
	return "", 0, false
}

func extractMultilineGTID(text string) string {
	re := regexp.MustCompile(`(?is)gtid_executed\s*=\s*(.+?)(?:\n\s*[a-z_]+\s*=|\z)`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	parts := strings.Split(m[1], "\n")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p), ","))
		if p == "" || strings.Contains(p, "=") {
			continue
		}
		cleaned = append(cleaned, p)
	}
	return strings.Join(cleaned, ",")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// CatalogBackupType maps xtrabackup checkpoint type to catalog enum.
func CatalogBackupType(cpType string) string {
	switch strings.ToLower(strings.TrimSpace(cpType)) {
	case "full-backuped", "full":
		return "FULL"
	case "incremental", "incremental-backuped":
		return "INCREMENTAL"
	default:
		if strings.Contains(strings.ToLower(cpType), "incremental") {
			return "INCREMENTAL"
		}
		return "FULL"
	}
}

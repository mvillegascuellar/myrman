package binlog

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/michaelvillegas/myrman/internal/parser"
)

// StartHint is used to pick the first remote dump file.
type StartHint struct {
	Requested     string
	LastCataloged string
	BackupBinlog  string // latest completed physical backup's binlog_file
}

// ChooseStartLog picks the first mysqlbinlog remote file that exists on the server.
// serverLogs: names from SHOW BINARY LOGS, oldest first.
func ChooseStartLog(h StartHint, serverLogs []string) (string, error) {
	if len(serverLogs) == 0 {
		return "", fmt.Errorf("SHOW BINARY LOGS returned no files; check log_bin and privileges (REPLICATION CLIENT)")
	}
	index := map[string]int{}
	for i, n := range serverLogs {
		index[n] = i
	}

	if h.Requested != "" {
		if _, ok := index[h.Requested]; !ok {
			return "", fmt.Errorf("binlog %q is not in the server index (available: %s)", h.Requested, strings.Join(serverLogs, ", "))
		}
		return h.Requested, nil
	}

	if h.LastCataloged != "" {
		if i, ok := index[h.LastCataloged]; ok {
			if i+1 < len(serverLogs) {
				return serverLogs[i+1], nil
			}
			return serverLogs[i], nil
		}
		lastSeq, err := parser.SequenceFromFilename(h.LastCataloged)
		if err == nil {
			for _, n := range serverLogs {
				seq, e := parser.SequenceFromFilename(n)
				if e == nil && seq > lastSeq {
					return n, nil
				}
			}
		}
	}

	if h.BackupBinlog != "" {
		if _, ok := index[h.BackupBinlog]; ok {
			return h.BackupBinlog, nil
		}
		bseq, err := parser.SequenceFromFilename(h.BackupBinlog)
		if err == nil {
			for _, n := range serverLogs {
				seq, e := parser.SequenceFromFilename(n)
				if e == nil && seq >= bseq {
					return n, nil
				}
			}
		}
	}

	return serverLogs[0], nil
}

func logsFrom(serverLogs []string, start string) []string {
	for i, n := range serverLogs {
		if n == start {
			return serverLogs[i:]
		}
	}
	return serverLogs
}

func isAnonymousGTIDDumpError(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "anonymous transaction") && strings.Contains(s, "gtid_mode")
}

func parseShowBinaryLogs(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if strings.EqualFold(name, "Log_name") {
			continue
		}
		names = append(names, name)
	}
	return names
}

func queryBinaryLogs(defaultsFile string) ([]string, error) {
	cmd := exec.Command("mysql",
		"--defaults-file="+defaultsFile,
		"-N", "-B",
		"-e", "SHOW BINARY LOGS",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("SHOW BINARY LOGS: %w: %s", err, strings.TrimSpace(string(out)))
	}
	names := parseShowBinaryLogs(string(out))
	if len(names) == 0 {
		return nil, fmt.Errorf("SHOW BINARY LOGS returned no files: %s", strings.TrimSpace(string(out)))
	}
	return names, nil
}

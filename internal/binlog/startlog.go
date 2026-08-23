package binlog

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/michaelvillegas/myrman/internal/parser"
)

// ChooseStartLog picks the first mysqlbinlog remote file that exists on the server.
// requested: env/config override. lastCataloged: last COMPLETED archive filename.
// serverLogs: names from SHOW BINARY LOGS, oldest first.
func ChooseStartLog(requested, lastCataloged string, serverLogs []string) (string, error) {
	if len(serverLogs) == 0 {
		return "", fmt.Errorf("SHOW BINARY LOGS returned no files; check log_bin and privileges (REPLICATION CLIENT)")
	}
	index := map[string]int{}
	for i, n := range serverLogs {
		index[n] = i
	}

	if requested != "" {
		if _, ok := index[requested]; !ok {
			return "", fmt.Errorf("binlog %q is not in the server index (available: %s)", requested, strings.Join(serverLogs, ", "))
		}
		return requested, nil
	}

	if lastCataloged != "" {
		if i, ok := index[lastCataloged]; ok {
			// Resume from the same file if it is the current last (still growing),
			// otherwise start at the next file after the one we already archived.
			if i+1 < len(serverLogs) {
				return serverLogs[i+1], nil
			}
			return serverLogs[i], nil
		}
		lastSeq, err := parser.SequenceFromFilename(lastCataloged)
		if err == nil {
			for _, n := range serverLogs {
				seq, e := parser.SequenceFromFilename(n)
				if e == nil && seq > lastSeq {
					return n, nil
				}
			}
		}
		// Cataloged file was purged and nothing newer remains — start at oldest available.
	}

	return serverLogs[0], nil
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

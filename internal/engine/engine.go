package engine

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Resolve returns the backup binary name: xtrabackup or mariabackup.
func Resolve(preference string) (string, error) {
	pref := strings.ToLower(strings.TrimSpace(preference))
	switch pref {
	case "xtrabackup", "mariabackup":
		if _, err := exec.LookPath(pref); err != nil {
			return "", fmt.Errorf("%s not found on PATH: %w", pref, err)
		}
		return pref, nil
	case "", "auto":
		if _, err := exec.LookPath("xtrabackup"); err == nil {
			return "xtrabackup", nil
		}
		if _, err := exec.LookPath("mariabackup"); err == nil {
			return "mariabackup", nil
		}
		return "", fmt.Errorf("neither xtrabackup nor mariabackup found on PATH")
	default:
		return "", fmt.Errorf("unknown backup_tool %q", preference)
	}
}

// PrepareBinary is the same tool used for --prepare.
func PrepareBinary(tool string) string { return tool }

var helpCache sync.Map // tool -> help text

func toolHelp(tool string) string {
	if v, ok := helpCache.Load(tool); ok {
		return v.(string)
	}
	out, err := exec.Command(tool, "--help").CombinedOutput()
	text := string(out)
	if err != nil && text == "" {
		return ""
	}
	helpCache.Store(tool, text)
	return text
}

// SupportsFlag reports whether `tool --help` mentions the given flag name
// (with or without leading dashes), e.g. "binlog-info".
func SupportsFlag(tool, flag string) bool {
	flag = strings.TrimLeft(flag, "-")
	help := toolHelp(tool)
	if help == "" {
		return false
	}
	return strings.Contains(help, "--"+flag)
}

// BackupFlags returns tool-specific extra backup arguments that are safe
// for this binary version (PXB 8.0 dropped --binlog-info; 8.4 still has it).
func BackupFlags(tool string) []string {
	var args []string
	if SupportsFlag(tool, "binlog-info") {
		args = append(args, "--binlog-info=ON")
	}
	if tool == "xtrabackup" && SupportsFlag(tool, "no-server-version-check") {
		args = append(args, "--no-server-version-check")
	}
	return args
}

package engine

import (
	"fmt"
	"os/exec"
	"strings"
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

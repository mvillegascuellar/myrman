package config

import (
	"fmt"
	"os"
	"strings"
)

// ResolveMySQLPassword returns the effective password.
// Priority: MYRMAN_MYSQL_PASSWORD env > cfg.MySQL.Password (catalog).
func ResolveMySQLPassword(cfg *Config) string {
	if p := os.Getenv("MYRMAN_MYSQL_PASSWORD"); p != "" {
		return p
	}
	return cfg.MySQL.Password
}

// RequireMySQLPassword ensures a password is available and sets cfg.MySQL.Password.
func RequireMySQLPassword(cfg *Config) error {
	pw := ResolveMySQLPassword(cfg)
	if pw == "" {
		return fmt.Errorf("MySQL password not set: sudo clears the environment, so MYRMAN_MYSQL_PASSWORD from your shell is not visible. Use one of:\n"+
			"  sudo MYRMAN_MYSQL_PASSWORD='...' ./myrman backup full\n"+
			"  sudo -E ./myrman backup full\n"+
			"  sudo ./myrman config set mysql.password='...'   # then retry")
	}
	cfg.MySQL.Password = pw
	return nil
}

// WriteClientDefaultsFile writes a 0600 defaults file that xtrabackup/mysqlbinlog
// can take as the sole --defaults-file (first argv). It !include's the server
// defaults_file, optional extra file, then [client]/[xtrabackup] credentials.
// Caller must remove the file when done (cleanup).
func WriteClientDefaultsFile(cfg *Config) (path string, cleanup func(), err error) {
	pw := ResolveMySQLPassword(cfg)
	cfg.MySQL.Password = pw

	f, err := os.CreateTemp("", "myrman-client-*.cnf")
	if err != nil {
		return "", nil, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }

	if err := os.Chmod(path, 0o600); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}

	user := cnfQuote(cfg.MySQL.User)
	password := cnfQuote(pw)
	host := cnfQuote(cfg.MySQL.Host)
	port := cfg.MySQL.Port
	if port == 0 {
		port = 3306
	}

	var b strings.Builder
	// xtrabackup requires --defaults-file OR --defaults-extra-file as argv[1],
	// never both. Fold the server cnf in via !include so we pass a single
	// --defaults-file that still carries datadir/innodb settings.
	if cfg.DefaultsFile != "" {
		fmt.Fprintf(&b, "!include %s\n\n", cfg.DefaultsFile)
	}
	// Include any user-provided extra defaults first so our credentials still win
	// when sections are re-declared below (later keys override in MySQL option files
	// within the same group only for last occurrence — we put ours last).
	if cfg.MySQL.DefaultsExtraFile != "" {
		raw, err := os.ReadFile(cfg.MySQL.DefaultsExtraFile)
		if err != nil {
			_ = f.Close()
			cleanup()
			return "", nil, fmt.Errorf("read mysql.defaults_extra_file: %w", err)
		}
		b.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	for _, section := range []string{"client", "mysql", "xtrabackup", "mysqldump"} {
		fmt.Fprintf(&b, "[%s]\n", section)
		if cfg.MySQL.User != "" {
			fmt.Fprintf(&b, "user=%s\n", user)
		}
		if pw != "" {
			fmt.Fprintf(&b, "password=%s\n", password)
		}
		if cfg.MySQL.Host != "" {
			fmt.Fprintf(&b, "host=%s\n", host)
		}
		fmt.Fprintf(&b, "port=%d\n\n", port)
	}

	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func cnfQuote(s string) string {
	if s == "" {
		return s
	}
	if strings.ContainsAny(s, " \"'#\\\t") {
		r := strings.ReplaceAll(s, `\`, `\\`)
		r = strings.ReplaceAll(r, `"`, `\"`)
		return `"` + r + `"`
	}
	return s
}

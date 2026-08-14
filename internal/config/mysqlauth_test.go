package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/michaelvillegas/myrman/internal/config"
)

func TestRequireMySQLPasswordFromEnv(t *testing.T) {
	cfg := config.Defaults()
	t.Setenv("MYRMAN_MYSQL_PASSWORD", "")
	if err := config.RequireMySQLPassword(cfg); err == nil {
		t.Fatal("expected error when password missing")
	}
	t.Setenv("MYRMAN_MYSQL_PASSWORD", "secret")
	if err := config.RequireMySQLPassword(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.Password != "secret" {
		t.Fatal(cfg.MySQL.Password)
	}
}

func TestRequireMySQLPasswordFromConfig(t *testing.T) {
	t.Setenv("MYRMAN_MYSQL_PASSWORD", "")
	cfg := config.Defaults()
	cfg.MySQL.Password = "from-catalog"
	if err := config.RequireMySQLPassword(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestWriteClientDefaultsFile(t *testing.T) {
	t.Setenv("MYRMAN_MYSQL_PASSWORD", "p#ass\"word")
	cfg := config.Defaults()
	cfg.MySQL.User = "backup"
	cfg.MySQL.Host = "127.0.0.1"
	cfg.MySQL.Port = 3306
	path, cleanup, err := config.WriteClientDefaultsFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "[xtrabackup]") || !strings.Contains(text, "[client]") {
		t.Fatalf("missing sections: %s", text)
	}
	if !strings.Contains(text, "password=") {
		t.Fatal("missing password")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("permissions too open: %v", st.Mode())
	}
}

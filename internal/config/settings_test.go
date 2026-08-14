package config_test

import (
	"testing"

	"github.com/michaelvillegas/myrman/internal/config"
)

func TestToFromSettingsRoundTrip(t *testing.T) {
	cfg := config.Defaults()
	cfg.MySQL.Host = "10.1.2.3"
	cfg.MySQL.Port = 3307
	cfg.DefaultsFile = "/etc/mysql/my.cnf"
	cfg.Compression = "gzip"
	cfg.Retention.OffsiteDaily = 14

	kv := config.ToSettings(cfg)
	got, err := config.FromSettings(kv)
	if err != nil {
		t.Fatal(err)
	}
	if got.MySQL.Host != "10.1.2.3" || got.MySQL.Port != 3307 {
		t.Fatalf("mysql=%s:%d", got.MySQL.Host, got.MySQL.Port)
	}
	if got.DefaultsFile != "/etc/mysql/my.cnf" {
		t.Fatal(got.DefaultsFile)
	}
	if got.Compression != "gzip" {
		t.Fatal(got.Compression)
	}
	if got.Retention.OffsiteDaily != 14 {
		t.Fatal(got.Retention.OffsiteDaily)
	}
}

func TestSetFieldAndParseSetArg(t *testing.T) {
	cfg := config.Defaults()
	if err := config.SetField(cfg, "mysql.host", "db.local"); err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.Host != "db.local" {
		t.Fatal(cfg.MySQL.Host)
	}
	if err := config.SetField(cfg, "not.a.key", "x"); err == nil {
		t.Fatal("expected unknown key error")
	}
	k, v, err := config.ParseSetArg([]string{"mysql.port=3308"})
	if err != nil || k != "mysql.port" || v != "3308" {
		t.Fatalf("%s %s %v", k, v, err)
	}
	k, v, err = config.ParseSetArg([]string{"compression", "none"})
	if err != nil || k != "compression" || v != "none" {
		t.Fatalf("%s %s %v", k, v, err)
	}
}

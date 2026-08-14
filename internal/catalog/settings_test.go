package catalog_test

import (
	"testing"

	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
)

func TestSettingsRepo(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir + "/c.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := catalog.NewSettingsRepo(db)
	cfg := config.Defaults()
	cfg.MySQL.Host = "127.0.0.1"
	cfg.Catalog = dir + "/c.db"
	if err := repo.SetMany(t.Context(), config.ToSettings(cfg)); err != nil {
		t.Fatal(err)
	}
	n, err := repo.Count(t.Context())
	if err != nil || n == 0 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	if err := repo.Set(t.Context(), "mysql.host", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := repo.Get(t.Context(), "mysql.host")
	if err != nil || !ok || v != "192.168.1.1" {
		t.Fatalf("%v %v %v", v, ok, err)
	}
	kv, err := repo.GetAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.FromSettings(kv)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MySQL.Host != "192.168.1.1" {
		t.Fatal(loaded.MySQL.Host)
	}
}

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/michaelvillegas/myrman/internal/config"
)

// ObjectStore is the cloud/object abstraction.
type ObjectStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) (url string, err error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// LocalStore manages filesystem paths under Root.
type LocalStore struct {
	Root string
}

func NewLocalStore(root string) *LocalStore {
	return &LocalStore{Root: root}
}

func (l *LocalStore) Abs(relOrAbs string) string {
	if filepath.IsAbs(relOrAbs) {
		return relOrAbs
	}
	return filepath.Join(l.Root, relOrAbs)
}

func (l *LocalStore) WriteFile(path string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return 0, err
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func (l *LocalStore) Remove(path string) error {
	return os.Remove(path)
}

func (l *LocalStore) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Coordinator writes local artifacts and optionally uploads to offsite.
type Coordinator struct {
	Local  *LocalStore
	Cloud  ObjectStore
	Config *config.Config
}

func NewCoordinator(cfg *config.Config) (*Coordinator, error) {
	c := &Coordinator{
		Local:  NewLocalStore(cfg.Local.Root),
		Config: cfg,
	}
	if cfg.Cloud.Provider == "" || cfg.Cloud.Provider == "none" {
		return c, nil
	}
	store, err := NewObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	c.Cloud = store
	return c, nil
}

func (c *Coordinator) HasCloud() bool { return c.Cloud != nil }

// ObjectKey builds a hierarchical object key.
func (c *Coordinator) ObjectKey(artifact string, t time.Time) string {
	prefix := c.Config.Cloud.Prefix
	server := c.Config.Cloud.ServerName
	return fmt.Sprintf("%s/%s/%04d/%02d/%s", prefix, server, t.UTC().Year(), int(t.UTC().Month()), artifact)
}

// UploadFile uploads a local file to cloud; returns URL.
func (c *Coordinator) UploadFile(ctx context.Context, localPath, key string) (string, error) {
	if c.Cloud == nil {
		return "", fmt.Errorf("cloud storage not configured")
	}
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return c.Cloud.Put(ctx, key, f, st.Size())
}

// DownloadTo downloads object key to dest path.
func (c *Coordinator) DownloadTo(ctx context.Context, key, dest string) error {
	if c.Cloud == nil {
		return fmt.Errorf("cloud storage not configured")
	}
	rc, err := c.Cloud.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, rc)
	return err
}

// KeyFromURL is a best-effort extract; for MVP cloud_url stores the key or full URL.
func KeyFromURL(urlOrKey string) string {
	return urlOrKey
}

func NewObjectStore(cfg *config.Config) (ObjectStore, error) {
	switch cfg.Cloud.Provider {
	case "s3":
		return NewS3Store(cfg)
	case "gcs":
		return NewGCSStore(cfg)
	case "azure":
		return NewAzureStore(cfg)
	default:
		return nil, fmt.Errorf("unsupported cloud provider %q", cfg.Cloud.Provider)
	}
}

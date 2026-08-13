package storage

import (
	"context"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/michaelvillegas/myrman/internal/config"
)

type GCSStore struct {
	client *storage.Client
	bucket string
}

func NewGCSStore(cfg *config.Config) (*GCSStore, error) {
	if cfg.Cloud.GCS.Bucket == "" {
		return nil, fmt.Errorf("cloud.gcs.bucket is required")
	}
	client, err := storage.NewClient(context.Background())
	if err != nil {
		return nil, err
	}
	return &GCSStore{client: client, bucket: cfg.Cloud.GCS.Bucket}, nil
}

func (g *GCSStore) Put(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	w := g.client.Bucket(g.bucket).Object(key).NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("gs://%s/%s", g.bucket, key), nil
}

func (g *GCSStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	key = stripGSURL(g.bucket, key)
	return g.client.Bucket(g.bucket).Object(key).NewReader(ctx)
}

func (g *GCSStore) Delete(ctx context.Context, key string) error {
	key = stripGSURL(g.bucket, key)
	return g.client.Bucket(g.bucket).Object(key).Delete(ctx)
}

func (g *GCSStore) Exists(ctx context.Context, key string) (bool, error) {
	key = stripGSURL(g.bucket, key)
	_, err := g.client.Bucket(g.bucket).Object(key).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func stripGSURL(bucket, key string) string {
	prefix := fmt.Sprintf("gs://%s/", bucket)
	if strings.HasPrefix(key, prefix) {
		return strings.TrimPrefix(key, prefix)
	}
	return key
}

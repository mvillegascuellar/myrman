package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/michaelvillegas/myrman/internal/config"
)

type AzureStore struct {
	client    *azblob.Client
	container string
	account   string
}

func NewAzureStore(cfg *config.Config) (*AzureStore, error) {
	if cfg.Cloud.Azure.Container == "" {
		return nil, fmt.Errorf("cloud.azure.container is required")
	}
	account := cfg.Cloud.Azure.Account
	if account == "" {
		account = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	key := os.Getenv("AZURE_STORAGE_KEY")
	conn := os.Getenv("AZURE_STORAGE_CONNECTION_STRING")

	var client *azblob.Client
	var err error
	if conn != "" {
		client, err = azblob.NewClientFromConnectionString(conn, nil)
	} else if account != "" && key != "" {
		cred, e := azblob.NewSharedKeyCredential(account, key)
		if e != nil {
			return nil, e
		}
		svc := fmt.Sprintf("https://%s.blob.core.windows.net/", account)
		client, err = azblob.NewClientWithSharedKeyCredential(svc, cred, nil)
	} else {
		return nil, fmt.Errorf("set AZURE_STORAGE_CONNECTION_STRING or AZURE_STORAGE_ACCOUNT+AZURE_STORAGE_KEY")
	}
	if err != nil {
		return nil, err
	}
	return &AzureStore{client: client, container: cfg.Cloud.Azure.Container, account: account}, nil
}

func (a *AzureStore) Put(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	_, err := a.client.UploadStream(ctx, a.container, key, r, nil)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s", a.account, a.container, key), nil
}

func (a *AzureStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	key = stripAzureURL(a.account, a.container, key)
	resp, err := a.client.DownloadStream(ctx, a.container, key, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (a *AzureStore) Delete(ctx context.Context, key string) error {
	key = stripAzureURL(a.account, a.container, key)
	_, err := a.client.DeleteBlob(ctx, a.container, key, nil)
	return err
}

func (a *AzureStore) Exists(ctx context.Context, key string) (bool, error) {
	key = stripAzureURL(a.account, a.container, key)
	pager := a.client.NewListBlobsFlatPager(a.container, &azblob.ListBlobsFlatOptions{
		Prefix: &key,
	})
	if pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, b := range page.Segment.BlobItems {
			if b.Name != nil && *b.Name == key {
				return true, nil
			}
		}
	}
	return false, nil
}

func stripAzureURL(account, container, key string) string {
	prefix := fmt.Sprintf("https://%s.blob.core.windows.net/%s/", account, container)
	if strings.HasPrefix(key, prefix) {
		return strings.TrimPrefix(key, prefix)
	}
	return key
}

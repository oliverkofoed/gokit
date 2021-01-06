package filestorekit

import (
	"context"
	"time"
)

type Store interface {
	Get(ctx context.Context, path string) (content []byte, contentType string, err error)
	Put(ctx context.Context, path string, contentType string, content []byte) error
	Remove(ctx context.Context, path string) error
	GetURL(path string, expire time.Duration) (string, error)
}

package filestorekit

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"time"
)

type FSStore struct {
	fs fs.FS
}

func NewFS(fs fs.FS) *FSStore {
	return &FSStore{
		fs: fs,
	}
}

func (l *FSStore) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	buf, err := fs.ReadFile(l.fs, path[1:])
	if err != nil {
		return nil, "", err
	}

	return buf, http.DetectContentType(buf), nil
}

func (l *FSStore) Put(ctx context.Context, path string, contentType string, content []byte) error {
	return errors.New("FSStore does not implement Put()")
}

func (l *FSStore) Remove(ctx context.Context, path string) error {
	return errors.New("FSStore does not implement Remove()")
}

func (l *FSStore) GetURL(path string, expire time.Duration) (string, error) {
	return "", errors.New("FSStore does not implement Remove()")
}

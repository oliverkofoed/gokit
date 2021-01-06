package filestorekit

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/oliverkofoed/gokit/cachekit"
)

type CacheStore struct {
	underlying Store
	cache      *cachekit.Cache
}

func NewCache(cache *cachekit.Cache, underlying Store) *CacheStore {
	return &CacheStore{
		cache:      cache,
		underlying: underlying,
	}
}

func (s *CacheStore) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	// check cache
	cacheKey := []byte(fmt.Sprintf("file:%v", path))
	if cached := s.cache.Get(ctx, cacheKey); cached != nil {
		contentType = string(cached[:bytes.IndexByte(cached, 0)])
		content := cached[150:]
		return content, contentType, nil
	}

	// download from underlying store
	underlyingContent, underlyingContentType, err := s.underlying.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}

	// save to cache if found in s3
	serialized := make([]byte, 150+len(underlyingContent))
	copy(serialized, []byte(*&underlyingContentType))
	copy(serialized[150:], underlyingContent)
	s.cache.Set(ctx, cacheKey, serialized, time.Hour*24*30)

	return underlyingContent, underlyingContentType, nil
}

func (s *CacheStore) Put(ctx context.Context, path string, contentType string, content []byte) error {
	err := s.underlying.Put(ctx, path, contentType, content)
	if err != nil {
		return err
	}

	// save to cache
	cacheKey := []byte(fmt.Sprintf("file:%v", path))
	serialized := make([]byte, 150+len(content))
	copy(serialized, []byte(*&contentType))
	copy(serialized[150:], content)
	s.cache.Set(ctx, cacheKey, serialized, time.Hour*24*30)
	return nil
}

func (s *CacheStore) Remove(ctx context.Context, path string) error {
	cacheKey := []byte(fmt.Sprintf("file:%v", path))
	s.cache.Remove(ctx, cacheKey)
	return s.underlying.Remove(ctx, path)
}

func (s *CacheStore) GetURL(path string, expire time.Duration) (string, error) {
	return s.underlying.GetURL(path, expire)
}

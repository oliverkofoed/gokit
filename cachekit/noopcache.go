package cachekit

import (
	"context"
	"time"
)

func NewNoOpCache() *Cache {
	return &Cache{cacheStore: noopCacheStore{}}
}

type noopCacheStore struct{}

func (c noopCacheStore) remove(ctx context.Context, key []byte)                        {}
func (c noopCacheStore) set(ctx context.Context, key, value []byte, ttl time.Duration) {}
func (c noopCacheStore) get(ctx context.Context, key []byte) []byte {
	return nil
}

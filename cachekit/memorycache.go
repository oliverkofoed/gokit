package cachekit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"github.com/coocood/freecache"
	"github.com/oliverkofoed/gokit/logkit"
)

const memoryKeyArrLength = 1024

type MemoryCache struct {
	sync.RWMutex
	cache         *freecache.Cache
	prefixes      map[string][]byte
	prefixCounter uint32
	bytePool      *sync.Pool
}

func NewMemoryCache(byteSize int) *MemoryCache {
	return &MemoryCache{
		cache:    freecache.NewCache(byteSize),
		prefixes: make(map[string][]byte),
		bytePool: &sync.Pool{
			New: func() interface{} {
				return [memoryKeyArrLength]byte{}
			},
		},
	}
}

func (d *MemoryCache) GetCache(prefix string) *Cache {
	d.Lock()
	defer d.Unlock()

	prefixBytes, found := d.prefixes[prefix]
	if !found {
		d.prefixCounter++
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.LittleEndian, &d.prefixCounter)
		prefixBytes = buf.Bytes()
	}

	return &Cache{cacheStore: memoryCacheStore{prefix: prefixBytes, c: d.cache, bytePool: d.bytePool}}
}

type memoryCacheStore struct {
	prefix   []byte
	bytePool *sync.Pool
	c        *freecache.Cache
}

func (m memoryCacheStore) get(ctx context.Context, key []byte) []byte {
	ctx, done := logkit.Operation(ctx, "memorycache.get", logkit.Bytes("key", key))
	defer done()

	k := m.bytePool.Get()
	defer m.bytePool.Put(k)
	v, e := m.c.Get(getMemoryKey(k.([memoryKeyArrLength]byte), m.prefix, key))
	if e != nil {
		return nil
	}
	return v
}

func (m memoryCacheStore) set(ctx context.Context, key, value []byte, ttl time.Duration) {
	ctx, done := logkit.Operation(ctx, "memorycache.set", logkit.Bytes("key", key), logkit.Bytes("value", value), logkit.Duration("ttl", ttl))
	defer done()

	expireSeconds := 0
	if ttl > 0 {
		expireSeconds = int(ttl / time.Second)
	} else if ttl < 0 {
		m.remove(ctx, key)
		return
	}

	k := m.bytePool.Get()
	defer m.bytePool.Put(k)
	m.c.Set(getMemoryKey(k.([memoryKeyArrLength]byte), m.prefix, key), value, expireSeconds)
}

func (m memoryCacheStore) remove(ctx context.Context, key []byte) {
	ctx, done := logkit.Operation(ctx, "memorycache.remove", logkit.Bytes("key", key))
	defer done()

	k := m.bytePool.Get()
	defer m.bytePool.Put(k)
	m.c.Del(getMemoryKey(k.([memoryKeyArrLength]byte), m.prefix, key))
}

func getMemoryKey(arr [memoryKeyArrLength]byte, prefix, key []byte) []byte {
	lp := len(prefix)
	lk := len(key)
	copy(arr[:lp], prefix)

	// if the key is too long, hash it?
	if lp+lk > len(arr) {
		h := sha256.New()
		h.Write(key)
		return h.Sum(arr[0:lp])
	}

	copy(arr[lp:], key)
	return arr[:lp+lk]
}

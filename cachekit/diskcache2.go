package cachekit

import (
	"container/list"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oliverkofoed/gokit/logkit"
)

type DiskCache2Stats struct {
	Size         int64
	Count        int64
	Misses       int64
	Hits         int64
	Evictions    int64
	WrittenBytes int64
	ReadBytes    int64
	DeletedBytes int64
}

type dc2EvictCommand int

const (
	dc2EvictStop    dc2EvictCommand = iota
	dc2EvictTrigger dc2EvictCommand = iota

	dc2ExtPending             = ".pending"
	dc2FileMode   fs.FileMode = 0640
	dc2HeaderSize             = 8

	dc2HashSize = sha1.Size

	// these control how many items are evicted during the eviction pass
	// eviction will continue as long as actual size is above maxSize * threshold%
	dc2ExhaustedEvictionThreshold = 0.75 // this is triggered when Set() detects we went over 100% of capacity
	dc2PeriodicEvictionThreshold  = 0.95 // this is done around every hour
)

type DiskCache2 struct {
	basePath string
	maxSize  int64
	stats    DiskCache2Stats
	chEvict  chan dc2EvictCommand

	lru      *list.List
	metadata map[dc2Hash]*list.Element
	lock     sync.Mutex
}

type dc2Hash [dc2HashSize]byte

type dc2Store struct {
	prefix []byte
	cache  *DiskCache2
}

type dc2Item struct {
	keyHash dc2Hash
}

func NewDiskCache2(ctx context.Context, basePath string, maxSize int64) (*DiskCache2, error) {
	cache := &DiskCache2{
		basePath: basePath,
		maxSize:  maxSize,

		chEvict:  make(chan dc2EvictCommand),
		lru:      list.New(),
		metadata: make(map[dc2Hash]*list.Element),
	}

	if err := os.MkdirAll(cache.dataPath(), dc2FileMode); err != nil {
		_ = logkit.Error(ctx, "Error creating cache path", logkit.String("path", cache.dataPath()), logkit.Err(err))
	}

	// TODO: this directory cannot be safely shared by multiple processes (we need to maintain the total size), maybe add a lockfile
	// TODO: this could potentially run concurrently with other parts of startup
	cache.reconstructState(ctx)

	return cache, nil
}

func (cache *DiskCache2) reconstructState(ctx context.Context) {
	start := time.Now()
	modTimes := make(map[dc2Hash]time.Time)

	cache.lock.Lock()
	defer cache.lock.Unlock()

	defer func() {
		_ = logkit.Debug(ctx, "Cache state reconstructed", logkit.Duration("time", time.Since(start)), logkit.Int64("size", cache.stats.Size))
	}()

	err := filepath.Walk(cache.dataPath(), func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Ext(path) == dc2ExtPending {
			// leftover invalid entry
			_ = os.Remove(path)
			return nil
		}

		modified := info.ModTime()
		var hash dc2Hash

		if _, err := hex.Decode(hash[:], []byte(info.Name())); err != nil {
			_ = logkit.Warn(ctx, "Invalid cache item name", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		modTimes[hash] = modified

		item := dc2Item{
			keyHash: hash,
		}

		// TODO: this is worst quadratic (is it faster than allocating huge scratch buffer while discovering files, is it worth not keeping the metadata in memory at all times?)
		// TODO: verify that the initial LRU list is sorted properly

		node := cache.lru.Back()
		for node != nil && modTimes[node.Value.(dc2Item).keyHash].Before(modified) {
			node = node.Next()
		}

		if node != nil {
			node = cache.lru.InsertAfter(item, node)
		} else {
			node = cache.lru.PushBack(item)
		}

		cache.metadata[item.keyHash] = node

		cache.stats.Size += info.Size()
		cache.stats.Count++

		return nil
	})

	go cache.evictionLoop(ctx)

	if err != nil {
		_ = logkit.Warn(ctx, "Error while traversing cache data directory", logkit.String("path", cache.dataPath()), logkit.Err(err))
	}
}

func (cache *DiskCache2) evictionLoop(ctx context.Context) {
	_ = logkit.Debug(ctx, "Starting disk cache eviction")

	for {
		select {
		case command := <-cache.chEvict:
			switch command {
			case dc2EvictStop:
				_ = logkit.Debug(ctx, "Shutting down disk cache eviction")
				return
			case dc2EvictTrigger:
				_ = logkit.Debug(ctx, "Explicit eviction trigger")
				cache.evict(ctx, dc2ExhaustedEvictionThreshold)
				break
			}
			break
		case <-time.After(1 * time.Hour):
			_ = logkit.Debug(ctx, "Periodic eviction trigger")
			cache.evict(ctx, dc2PeriodicEvictionThreshold)
			break
		}
	}
}

func (cache *DiskCache2) evict(ctx context.Context, threshold float64) {
	thresholdSize := int64(float64(cache.maxSize) * threshold)

	for cache.stats.Size >= thresholdSize {
		item := func() dc2Item {
			cache.lock.Lock()
			defer cache.lock.Unlock()

			return cache.lru.Back().Value.(dc2Item)
		}()

		atomic.AddInt64(&cache.stats.Evictions, 1)
		cache.Remove(ctx, item.keyHash)
	}
}

func (cache *DiskCache2) dataPath() string {
	return filepath.Join(cache.basePath, "data")
}

func (cache *DiskCache2) itemKeyHash(prefix []byte, key []byte) dc2Hash {
	hash := sha1.New()
	_, _ = hash.Write(prefix)
	_, _ = hash.Write(key)

	var bytes []byte
	hash.Sum(bytes)

	var result dc2Hash
	copy(bytes, result[:])

	return result
}

func (cache *DiskCache2) itemPath(hash dc2Hash) string {
	// two-level directory structure should be enough to avoid too big directories
	hashStr := fmt.Sprintf("%x", hash[:])

	first := hashStr[0:2]
	second := hashStr[2:4]

	return filepath.Join(cache.dataPath(), first, second, hashStr)
}

func (cache *DiskCache2) Stats() DiskCache2Stats {
	return cache.stats
}

func (cache *DiskCache2) Close() {
}

func (cache *DiskCache2) GetCache(_ context.Context, prefix string) *Cache {
	return &Cache{cacheStore: dc2Store{prefix: []byte(prefix), cache: cache}}
}

func (cache *DiskCache2) touch(keyHash dc2Hash, path string, size int64) {
	func() {
		cache.lock.Lock()
		defer cache.lock.Unlock()

		if node := cache.metadata[keyHash]; node != nil {
			cache.lru.MoveToFront(node)
		} else {
			item := dc2Item{
				keyHash: keyHash,
			}

			cache.metadata[keyHash] = cache.lru.PushBack(item)
			cache.stats.Size += size

			if cache.stats.Size >= cache.maxSize {
				cache.chEvict <- dc2EvictTrigger
			}
		}
	}()

	now := time.Now()

	// this might fail if the file is evicted before we have a chance to update the timestamp
	// but this is probably an edge case not worth handling
	// TODO: maybe run this asynchronously
	_ = os.Chtimes(path, now, now)
}

func (cache *DiskCache2) Get(ctx context.Context, keyHash dc2Hash) []byte {
	path := cache.itemPath(keyHash)
	fp, err := os.Open(path)

	// TODO: do we need to worry about data integrity? maybe using a filesystem like ZFS would be enough

	if err != nil {
		atomic.AddInt64(&cache.stats.Misses, 1)
		return nil
	} else {
		var expiresAt int64

		if err := binary.Read(fp, binary.LittleEndian, &expiresAt); err != nil {
			_ = logkit.Warn(ctx, "Cache file exists but failed to read the header", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		if time.Now().After(time.Unix(expiresAt, 0)) {
			// stale entry is treated as a miss
			// TODO: the removal could probably be asynchronous
			atomic.AddInt64(&cache.stats.Misses, 1)
			cache.Remove(ctx, keyHash)
			return nil
		}

		data, err := io.ReadAll(fp)

		if err != nil {
			_ = logkit.Warn(ctx, "Cache file exists but failed to read the data", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		size := int64(len(data)) + dc2HeaderSize
		atomic.AddInt64(&cache.stats.ReadBytes, size)
		atomic.AddInt64(&cache.stats.Hits, 1)
		cache.touch(keyHash, path, size)

		return data
	}
}

func (cache *DiskCache2) Set(ctx context.Context, keyHash dc2Hash, value []byte, ttl time.Duration) {
	path := cache.itemPath(keyHash)
	pendingPath := path + dc2ExtPending
	size := int64(len(value)) + dc2HeaderSize

	_ = os.MkdirAll(filepath.Dir(path), dc2FileMode)
	pending, err := os.OpenFile(pendingPath, os.O_RDWR|os.O_CREATE, dc2FileMode)

	// TODO: this needs to check the previous object to maintain the correct size

	if err != nil {
		_ = logkit.Error(ctx, "Failed to create a cache file", logkit.String("path", pendingPath), logkit.Err(err))
		return
	}

	// defer is function-wide, and we want to close pending file before renaming
	func() {
		defer pending.Close()
		expiresAt := time.Now().Add(ttl).Unix()

		if err := binary.Write(pending, binary.LittleEndian, expiresAt); err != nil {
			_ = logkit.Error(ctx, "Failed to write the header into a cache file", logkit.String("path", pendingPath), logkit.Err(err))
			return
		}

		if _, err := pending.Write(value); err != nil {
			_ = logkit.Error(ctx, "Failed to write the data into a cache file", logkit.String("path", pendingPath), logkit.Err(err))
			return
		}

		atomic.AddInt64(&cache.stats.WrittenBytes, size)

		// errors here can't be handled very well
		_ = pending.Sync()
	}()

	// rename will remove the old file
	if err := os.Rename(pendingPath, path); err != nil {
		_ = logkit.Error(ctx, "Failed to rename pending cache file", logkit.String("path", pendingPath), logkit.Err(err))
		cache.Remove(ctx, keyHash)
		return
	}

	cache.touch(keyHash, path, size)
}

func (cache *DiskCache2) Remove(ctx context.Context, keyHash dc2Hash) {
	path := cache.itemPath(keyHash)
	pendingPath := path + dc2ExtPending

	stat, err := os.Stat(path)
	removeMetadata := func() bool {
		cache.lock.Lock()
		defer cache.lock.Unlock()

		if node := cache.metadata[keyHash]; node != nil {
			cache.stats.Size -= stat.Size()
			cache.stats.Count--
			cache.lru.Remove(node)
			delete(cache.metadata, keyHash)
			return true
		}

		return false
	}

	if err != nil {
		// TODO: not sure how likely this is to happen outside for other process removing the file from under us
		// there's not really much we can do about it either: this is the only place we can get the size from
		// (at the start I used to keep it in dc2Item but the increased memory usage might not be worth it)

		// sanity check: if the file doesn't exist then it shouldn't be in the metadata map either
		if removeMetadata() {
			_ = logkit.Error(ctx, "cache.metadata was inconsistent: file didn't exist, but metadata entry did", logkit.String("path", path))
		}
	} else {
		removeMetadata()

		if err := os.Remove(path); err != nil {
			_ = logkit.Error(ctx, "Failed to remove cache data file, this could mean disk or filesystem failure", logkit.String("path", path))
		}

		_ = os.Remove(pendingPath) // TODO: should this be sanity checked?
	}
}

func (store dc2Store) get(ctx context.Context, key []byte) []byte {
	keyHash := store.cache.itemKeyHash(store.prefix, key)
	return store.cache.Get(ctx, keyHash)
}

func (store dc2Store) set(ctx context.Context, key []byte, value []byte, ttl time.Duration) {
	// TODO: this could potentially run asynchronously

	keyHash := store.cache.itemKeyHash(store.prefix, key)
	store.cache.Set(ctx, keyHash, value, ttl)
}

func (store dc2Store) remove(ctx context.Context, key []byte) {
	// TODO: this could potentially run asynchronously

	keyHash := store.cache.itemKeyHash(store.prefix, key)
	store.cache.Remove(ctx, keyHash)
}

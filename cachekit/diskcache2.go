package cachekit

import (
	"container/heap"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/oliverkofoed/gokit/logkit"
)

type DiskCache2Stats struct {
	EstimatedSize  int64
	EstimatedCount int64

	Misses    int64
	Hits      int64
	Evictions int64

	WrittenBytes int64
	ReadBytes    int64
	DeletedBytes int64
}

type DiskCache2 struct {
	basePath string
	maxSize  int64
	stats    DiskCache2Stats

	chEvict chan dc2EvictCommand
}

type dc2EvictCommand int
type dc2Hash [dc2HashSize]byte

type dc2Store struct {
	prefix []byte
	cache  *DiskCache2
}

type dc2EvictionState struct {
	items []dc2CacheItem
}

type dc2CacheItem struct {
	hash dc2Hash
	time int64
}

const (
	dc2EvictStop        dc2EvictCommand = iota
	dc2EvictTriggerFast dc2EvictCommand = iota
	dc2EvictTriggerSlow dc2EvictCommand = iota
)

const (
	dc2ExtPending = ".pending"
	dc2FileMode   = 0640
	dc2DirMode    = 0750

	dc2HeaderSize = 8
	dc2HashSize   = sha1.Size

	// how long should we wait after each item
	dc2EvictFastThrottle = 0 * time.Millisecond
	dc2EvictSlowThrottle = 5 * time.Millisecond

	// these control how many items are evicted during the eviction pass
	// eviction will continue as long as actual size is above maxSize * threshold%
	dc2ExhaustedEvictionThreshold = 0.75 // this is triggered when Set() detects we went over 100% of capacity (estimated)
	dc2PeriodicEvictionThreshold  = 0.95 // this is done around every hour
)

func NewDiskCache2(ctx context.Context, basePath string, maxSize int64) (*DiskCache2, error) {
	cache := &DiskCache2{
		basePath: basePath,
		maxSize:  maxSize,

		chEvict: make(chan dc2EvictCommand, 1),
	}

	if err := os.MkdirAll(cache.dataPath(), dc2DirMode); err != nil {
		_ = logkit.Error(ctx, "Error creating cache path", logkit.String("path", cache.dataPath()), logkit.Err(err))
	}

	go cache.evictionLoop(ctx)

	// trigger initial eviction (both to estimate the size of the cache and to make sure we don't start over capacity)
	//
	// concurrent cache operations should be fine: the eviction might remove something it shouldn't have, or the cache might
	// take a bit too much space sometimes, but overall this should self-correct and not cause any incorrect behaviour
	cache.triggerEviction(dc2EvictTriggerFast)

	return cache, nil
}

func (cache *DiskCache2) Close() {
	cache.chEvict <- dc2EvictStop
}

func (cache *DiskCache2) triggerEviction(command dc2EvictCommand) {
	// this will be a no-op if eviction is already queued
	select {
	case cache.chEvict <- command:
		break
	default:
		break
	}
}

func (cache *DiskCache2) walkCache(ctx context.Context, fn func(path string, hash dc2Hash, info fs.FileInfo)) {
	start := time.Now()

	defer func() {
		_ = logkit.Debug(ctx, "Cache walk complete",
			logkit.Duration("time", time.Since(start)),
			logkit.Int64("size", cache.stats.EstimatedSize),
			logkit.Int64("count", cache.stats.EstimatedCount))
	}()

	atomic.StoreInt64(&cache.stats.EstimatedSize, 0)
	atomic.StoreInt64(&cache.stats.EstimatedCount, 0)

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

		var hash dc2Hash

		if _, err := hex.Decode(hash[:], []byte(info.Name())); err != nil {
			_ = logkit.Warn(ctx, "Invalid cache item name", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		atomic.AddInt64(&cache.stats.EstimatedSize, info.Size())
		atomic.AddInt64(&cache.stats.EstimatedCount, 1)

		fn(path, hash, info)

		return nil
	})

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
			case dc2EvictTriggerFast:
				_ = logkit.Debug(ctx, "Explicit eviction trigger (fast)")
				cache.evict(ctx, dc2PeriodicEvictionThreshold, dc2EvictFastThrottle)
				break
			case dc2EvictTriggerSlow:
				_ = logkit.Debug(ctx, "Explicit eviction trigger (slow)")
				cache.evict(ctx, dc2ExhaustedEvictionThreshold, dc2EvictSlowThrottle)
				break
			}
			break
		case <-time.After(1 * time.Hour):
			_ = logkit.Debug(ctx, "Periodic eviction trigger")
			cache.evict(ctx, dc2PeriodicEvictionThreshold, dc2EvictSlowThrottle)
			break
		}
	}
}

func (cache *DiskCache2) evict(ctx context.Context, threshold float64, throttle time.Duration) {
	thresholdSize := int64(float64(cache.maxSize) * threshold)
	state := &dc2EvictionState{
		items: make([]dc2CacheItem, 0, 64),
	}

	cache.walkCache(ctx, func(path string, hash dc2Hash, info fs.FileInfo) {
		item := dc2CacheItem{
			hash: hash,
			time: atime(info).Unix(),
		}

		// we don't need linked list node moving now, so a slice with heap invariant should perform better
		heap.Push(state, item)
	})

	for cache.stats.EstimatedSize >= thresholdSize {
		item := heap.Pop(state).(dc2CacheItem)

		if cache.Remove(ctx, item.hash) {
			atomic.AddInt64(&cache.stats.Evictions, 1)
			<-time.After(throttle)
		}
	}
}

func (cache *DiskCache2) dataPath() string {
	return filepath.Join(cache.basePath, "data")
}

func (cache *DiskCache2) itemKeyHash(prefix []byte, key []byte) dc2Hash {
	hash := sha1.New()
	_, _ = hash.Write(prefix)
	_, _ = hash.Write(key)

	bytes := hash.Sum(make([]byte, 0))

	var result dc2Hash
	copy(result[:], bytes)

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

func (cache *DiskCache2) GetCache(_ context.Context, prefix string) *Cache {
	return &Cache{
		cacheStore: dc2Store{
			prefix: []byte(prefix),
			cache:  cache,
		},
	}
}

func (cache *DiskCache2) Get(ctx context.Context, keyHash dc2Hash) []byte {
	path := cache.itemPath(keyHash)

	// TODO: do we need to worry about data integrity? maybe using a filesystem like ZFS would be enough

	data := func() []byte {
		fp, err := os.Open(path)
		defer fp.Close()

		if err != nil {
			return nil
		}

		var expiresAt int64

		if err := binary.Read(fp, binary.LittleEndian, &expiresAt); err != nil {
			_ = logkit.Warn(ctx, "Cache file exists but failed to read the header", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		if expiresAt != 0 && time.Now().After(time.Unix(expiresAt, 0)) {
			// stale entry
			return nil
		}

		data, err := io.ReadAll(fp)

		if err != nil {
			_ = logkit.Warn(ctx, "Cache file exists but failed to read the data", logkit.String("path", path), logkit.Err(err))
			return nil
		}

		return data
	}()

	if data == nil {
		// _ = logkit.Debug(ctx, "Cache miss", logkit.String("path", path))

		atomic.AddInt64(&cache.stats.Misses, 1)

		// TODO: async removal?
		cache.Remove(ctx, keyHash)
	} else {
		// _ = logkit.Debug(ctx, "Cache hit", logkit.String("path", path))

		size := int64(len(data)) + dc2HeaderSize
		atomic.AddInt64(&cache.stats.ReadBytes, size)
		atomic.AddInt64(&cache.stats.Hits, 1)
	}

	return data
}

func (cache *DiskCache2) Set(ctx context.Context, keyHash dc2Hash, value []byte, ttl time.Duration) {
	path := cache.itemPath(keyHash)
	pendingPath := path + dc2ExtPending

	size := int64(len(value)) + dc2HeaderSize
	var oldSize int64
	var oldCount int64

	if stat, err := os.Stat(path); err == nil {
		oldSize = stat.Size()
		oldCount = 1
	}

	_ = os.MkdirAll(filepath.Dir(path), dc2FileMode)
	pending, err := os.OpenFile(pendingPath, os.O_RDWR|os.O_CREATE, dc2FileMode)

	if err != nil {
		_ = logkit.Error(ctx, "Failed to create a cache file", logkit.String("path", pendingPath), logkit.Err(err))
		return
	}

	// defer is function-wide, and we want to close pending file before renaming
	result := func() bool {
		defer pending.Close()
		var expiresAt int64

		if ttl != 0 {
			expiresAt = time.Now().Add(ttl).Unix()
		}

		if err := binary.Write(pending, binary.LittleEndian, expiresAt); err != nil {
			_ = logkit.Error(ctx, "Failed to write the header into a cache file", logkit.String("path", pendingPath), logkit.Err(err))
			return false
		}

		if _, err := pending.Write(value); err != nil {
			_ = logkit.Error(ctx, "Failed to write the data into a cache file", logkit.String("path", pendingPath), logkit.Err(err))
			return false
		}

		atomic.AddInt64(&cache.stats.WrittenBytes, size)

		// errors here can't be handled very well
		_ = pending.Sync()
		return true
	}()

	if !result {
		// corrupted file
		_ = os.Remove(pendingPath)
		return
	}

	// rename will remove the old file
	if err := os.Rename(pendingPath, path); err != nil {
		_ = logkit.Error(ctx, "Failed to rename pending cache file", logkit.String("path", pendingPath), logkit.Err(err))
		cache.Remove(ctx, keyHash)
		return
	}

	// _ = logkit.Debug(ctx, "Created a cache item", logkit.String("path", path), logkit.Int64("size", size))

	atomic.AddInt64(&cache.stats.EstimatedCount, 1-oldCount)

	if atomic.AddInt64(&cache.stats.EstimatedSize, size-oldSize) >= cache.maxSize {
		cache.triggerEviction(dc2EvictTriggerSlow)
	}
}

func (cache *DiskCache2) Remove(ctx context.Context, keyHash dc2Hash) bool {
	path := cache.itemPath(keyHash)
	pendingPath := path + dc2ExtPending

	info, err := os.Stat(path)

	if err == nil {
		// _ = logkit.Debug(ctx, "Removing cache item", logkit.String("path", path), logkit.Int64("size", info.Size()), logkit.Time("time", atime(info)))

		if err := os.Remove(path); err != nil {
			_ = logkit.Error(ctx, "Failed to remove cache data file, this could mean disk or filesystem failure", logkit.String("path", path), logkit.Err(err))
		}

		_ = os.Remove(pendingPath) // TODO: should this be sanity checked?

		atomic.AddInt64(&cache.stats.EstimatedSize, -info.Size())
		atomic.AddInt64(&cache.stats.EstimatedCount, -1)
		atomic.AddInt64(&cache.stats.DeletedBytes, info.Size())

		return true
	} else {
		// _ = logkit.Debug(ctx, "NOT removing cache item", logkit.String("path", path))
	}

	return false
}

func (store dc2Store) get(ctx context.Context, key []byte) []byte {
	keyHash := store.cache.itemKeyHash(store.prefix, key)
	return store.cache.Get(ctx, keyHash)
}

func (store dc2Store) set(ctx context.Context, key []byte, value []byte, ttl time.Duration) {
	keyHash := store.cache.itemKeyHash(store.prefix, key)
	store.cache.Set(ctx, keyHash, value, ttl)
}

func (store dc2Store) remove(ctx context.Context, key []byte) {
	keyHash := store.cache.itemKeyHash(store.prefix, key)
	store.cache.Remove(ctx, keyHash)
}

func (state *dc2EvictionState) Push(value interface{}) {
	state.items = append(state.items, value.(dc2CacheItem))
}

func (state *dc2EvictionState) Pop() interface{} {
	n := state.Len()
	item := state.items[n-1]
	state.items = state.items[0 : n-1]
	return item
}

func (state *dc2EvictionState) Swap(i, j int) {
	state.items[i], state.items[j] = state.items[j], state.items[i]
}

func (state *dc2EvictionState) Less(i, j int) bool {
	return state.items[i].time < state.items[j].time
}

func (state *dc2EvictionState) Len() int {
	return len(state.items)
}

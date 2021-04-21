package cachekit

import (
	"bytes"
	"context"
	"encoding/binary"
	"runtime/debug"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/oliverkofoed/gokit/logkit"
)

var accessPrefix = []byte{0, 255, 0, 255}
var idBucket = []byte{1}
var cacheBucket = []byte{0}

type DiskCache struct {
	db     *bolt.DB
	closed bool
	lock   sync.RWMutex
}

func NewDiskCache(ctx context.Context, filename string, maxSize int64) (*DiskCache, error) {
	db, err := bolt.Open(filename, 0600, nil)
	if err != nil {
		return nil, err
	}

	// ensure buckets exists
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(cacheBucket)
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(idBucket)
		return err
	})
	if err != nil {
		return nil, err
	}

	cache := &DiskCache{db: db}
	go evictionLoop(ctx, cache, maxSize)
	return cache, nil
}

func evictionLoop(ctx context.Context, cache *DiskCache, maxSize int64) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if !cache.closed {
		cache.Evict(ctx, maxSize)

		time.AfterFunc(10*time.Second, func() {
			evictionLoop(ctx, cache, maxSize)
		})
	}
}

func (d *DiskCache) Close() error {
	d.lock.Lock()
	defer d.lock.Unlock()
	err := d.db.Close()
	d.closed = true
	return err
}

func (d *DiskCache) GetCache(ctx context.Context, prefix string) *Cache {
	var prefixBytes []byte
	err := d.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(idBucket)

		dbPrefix := bucket.Get([]byte(prefix))

		if dbPrefix == nil {
			ctr := bucket.Get([]byte{0})
			if ctr == nil {
				ctr = []byte{0, 0}
			}

			var v uint16
			buf := bytes.NewBuffer(ctr)
			binary.Read(buf, binary.LittleEndian, &v) // read ctr
			v++
			buf = new(bytes.Buffer)
			binary.Write(buf, binary.LittleEndian, &v) // read ctr
			dbPrefix = buf.Bytes()
			bucket.Put([]byte(prefix), dbPrefix)
			bucket.Put([]byte{0}, dbPrefix)
		}

		prefixBytes = make([]byte, len(dbPrefix), len(dbPrefix))
		copy(prefixBytes, dbPrefix)

		return nil
	})
	if err != nil || prefixBytes == nil {
		logkit.Error(ctx, "Error getting cache", logkit.String("prefix", prefix), logkit.Err(err))
		return NewNoOpCache()
	}

	return &Cache{cacheStore: diskCacheStore{prefix: prefixBytes, db: d.db}}
}

func (d *DiskCache) Evict(ctx context.Context, maxSize int64) {
	log, done := logkit.Operation(ctx, "diskcache.evict", logkit.Int64("maxSize", maxSize))
	defer done()

	var offset []byte
	var complete bool
	seen := make(map[string]bool)
	totalSize := int64(0)
	deletedEntries := 0
	deletedLeftovers := 0
	batchSize := 100

	for !complete {
		err := d.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket(cacheBucket)

			accessLength := len(accessPrefix)
			c := bucket.Cursor()
			now := time.Now().UnixNano()
			batchOperations := 0

			k, v := c.Seek(accessPrefix)
			if offset != nil {
				k, v = c.Seek(offset)
				k, v = c.Next()
			}

			for ; bytes.HasPrefix(k, accessPrefix); k, v = c.Next() {
				if len(v) != 16 {
					continue
				}
				var length int64
				var expires int64
				key := k[accessLength+8:]
				buf := bytes.NewBuffer(v)
				binary.Read(buf, binary.LittleEndian, &length)
				binary.Read(buf, binary.LittleEndian, &expires)

				//fmt.Println(fmt.Sprintf(string(key)), expires)

				if _, alreadyProcessed := seen[string(key)]; !alreadyProcessed {
					delete := expires < now || (totalSize+length) >= maxSize
					if !delete {
						totalSize += length
					}

					if delete {
						bucket.Delete(key) // delete data
						bucket.Delete(k)   // delete access pointer
						deletedEntries++
						batchOperations++
					}

					seen[string(key)] = true
				} else {
					// already seen this acces pointer, get rid of it
					bucket.Delete(k)
					batchOperations++
					deletedLeftovers++
				}

				offset = k
				if batchOperations >= batchSize {
					return nil
				}
			}

			complete = true
			return nil
		})
		if err != nil {
			log.Error("Error evicting from DiskCache", logkit.Err(err))
			break
		}
	}

	logkit.Debug(ctx, "diskcache.evict.complete",
		logkit.Int64("alivebytes", totalSize),
		logkit.Int64("deleted", int64(deletedEntries)),
		logkit.Int64("deletedleftoveraccespointers", int64(deletedLeftovers)))
}

type diskCacheStore struct {
	prefix []byte
	db     *bolt.DB
}

func (d diskCacheStore) get(ctx context.Context, key []byte) []byte {
	log, done := logkit.Operation(ctx, "diskcache.get", logkit.Bytes("key", key))
	defer done()

	// get prefixedKey
	keyBuf := new(bytes.Buffer)
	keyBuf.Write(d.prefix)
	keyBuf.Write(key)
	key = keyBuf.Bytes()

	// get result
	var result []byte
	result = nil
	err := d.db.Update(func(tx *bolt.Tx) error {
		// get the bucket
		b := tx.Bucket(cacheBucket)

		// read the value
		r := b.Get(key)

		// check times
		if r != nil && len(r) >= 16 {
			now := time.Now()

			// read time fields
			var lastAccess int64
			var expires int64
			buf := bytes.NewReader(r)
			binary.Read(buf, binary.LittleEndian, &lastAccess)
			binary.Read(buf, binary.LittleEndian, &expires)

			// if the key has expired, delete it
			if expires < now.UnixNano() {
				b.Delete(key)
				result = nil
				return nil
			}

			// slice out the times from the value
			r = r[16:]

			// copy value, so it's valid outside transaction
			result = make([]byte, len(r))
			copy(result, r)

			// if the key has not been accessed 10 minutes, update access time.
			if lastAccess < now.Add(-10*time.Minute).UnixNano() {
				d.write(tx, b, key, r, time.Now().UnixNano(), expires, lastAccess)
			}
		}

		return nil
	})
	if err != nil {
		log.Error("Error reading from DiskCache", logkit.Err(err))
		return nil
	}

	return result
}

func (d diskCacheStore) set(ctx context.Context, key, value []byte, ttl time.Duration) {
	log, done := logkit.Operation(ctx, "diskcache.set", logkit.Bytes("key", key), logkit.Bytes("value", value), logkit.Duration("ttl", ttl))
	defer done()

	// 0 = far expires
	if ttl == 0 {
		ttl = time.Hour * 24 * 365 * 10 // 10 years
	}

	// check times
	now := time.Now()
	expires := now.Add(ttl).UnixNano()

	// get prefixedKey
	keyBuf := new(bytes.Buffer)
	keyBuf.Write(d.prefix)
	keyBuf.Write(key)
	key = keyBuf.Bytes()

	// don't write nil values
	if value == nil {
		log.Warn("Tried to write nil value to cache, this is often a mistake", logkit.String("stack", string(debug.Stack())))
		return
	}

	// write to db
	err := d.db.Update(func(tx *bolt.Tx) error {
		// get the bucket
		b := tx.Bucket(cacheBucket)

		// write the value
		d.write(tx, b, key, value, now.UnixNano(), expires, 0)

		return nil
	})
	if err != nil {
		log.Error("Error writing to DiskCache", logkit.Err(err))
	}
}

func (d *diskCacheStore) write(tx *bolt.Tx, b *bolt.Bucket, key, value []byte, now int64, expires int64, lastAccess int64) {
	// write the value
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, now)     // last access
	binary.Write(buf, binary.LittleEndian, expires) // expires
	buf.Write(value)
	valueBytes := buf.Bytes()
	b.Put(key, valueBytes)

	// write the new access pointer
	buf = new(bytes.Buffer)
	buf.Write(accessPrefix)
	binary.Write(buf, binary.BigEndian, -now)
	buf.Write(key)
	valBuf := new(bytes.Buffer)
	binary.Write(valBuf, binary.LittleEndian, int64(len(key)+len(value)))
	binary.Write(valBuf, binary.LittleEndian, expires)
	b.Put(buf.Bytes(), valBuf.Bytes())

	// remove old access pointer
	if lastAccess != 0 {
		buf = new(bytes.Buffer)
		buf.Write(accessPrefix)
		binary.Write(buf, binary.BigEndian, -lastAccess)
		buf.Write(key)

		b.Delete(buf.Bytes())
	}
}

func (d diskCacheStore) remove(ctx context.Context, key []byte) {
	log, done := logkit.Operation(ctx, "diskcache.remove", logkit.Bytes("key", key))
	defer done()

	// get prefixedKey
	keyBuf := new(bytes.Buffer)
	keyBuf.Write(d.prefix)
	keyBuf.Write(key)
	key = keyBuf.Bytes()

	// delete key
	err := d.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(cacheBucket)
		bucket.Delete(key)
		return nil
	})
	if err != nil {
		log.Error("Error deleting from DiskCache", logkit.Err(err))
	}
}

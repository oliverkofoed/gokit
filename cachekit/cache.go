package cachekit

import (
	"bytes"
	"context"
	"encoding/gob"
	"time"
)

var nilValue = []byte{0, 255, 1, 5, 29, 4}

type Cache struct {
	cacheStore
}

type cacheStore interface {
	get(ctx context.Context, key []byte) []byte
	set(ctx context.Context, key []byte, value []byte, ttl time.Duration)
	remove(ctx context.Context, key []byte)
}

func (c Cache) Get(ctx context.Context, key []byte) []byte {
	return c.get(ctx, key)
}

func (c Cache) GetFunc(ctx context.Context, key []byte, ttl time.Duration, f func(key []byte) []byte) []byte {
	val := c.get(ctx, key)
	if val == nil {
		val = f(key)
		if val == nil {
			val = nilValue
		}
		c.cacheStore.set(ctx, key, val, ttl)
	}
	if isNil(val) {
		return nil
	}
	return val
}

func (c Cache) GetFuncErr(ctx context.Context, key []byte, ttl time.Duration, f func(key []byte) ([]byte, error)) ([]byte, error) {
	val := c.get(ctx, key)
	if val == nil {
		var err error
		val, err = f(key)
		if err != nil {
			return nil, err
		}
		if val == nil {
			val = nilValue
		}
		c.cacheStore.set(ctx, key, val, ttl)
	}
	if isNil(val) {
		return nil, nil
	}
	return val, nil
}

func (c Cache) GetGobFuncErr(ctx context.Context, key []byte, ttl time.Duration, output interface{}, f func(key []byte) (interface{}, error)) error {
	b, err := c.GetFuncErr(ctx, key, ttl, func(key []byte) ([]byte, error) {
		v, err := f(key)
		if err != nil {
			return nil, err
		}

		var buf bytes.Buffer
		err = gob.NewEncoder(&buf).Encode(v)
		if err != nil {
			return nil, err
		}

		return buf.Bytes(), nil
	})
	if err != nil {
		return err
	}

	dec := gob.NewDecoder(bytes.NewBuffer(b))
	err = dec.Decode(output)
	if err != nil {
		return err
	}

	return nil
}

func (c Cache) Set(ctx context.Context, key, value []byte, ttl time.Duration) {
	c.set(ctx, key, value, ttl)
}

func (c Cache) Remove(ctx context.Context, key []byte) {
	c.remove(ctx, key)
}

func isNil(val []byte) bool {
	if val == nil {
		return true
	}
	if len(val) == len(nilValue) {
		for i, v := range val {
			if v != nilValue[i] {
				return false
			}
		}
		return true
	}
	return false
}

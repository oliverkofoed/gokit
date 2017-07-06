package cachekit

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func cachetest(c1 *Cache, c2 *Cache, t *testing.T) bool {
	ctx := context.Background()

	// get "hello", expect nil
	v := c1.Get(ctx, []byte("hello"))
	if v != nil {
		t.Error("expected nil")
		return false
	}

	// set "hello"=world, get:expect "world"
	c1.Set(ctx, []byte("hello"), []byte("world"), 10*time.Hour)
	v = c1.Get(ctx, []byte("hello"))
	if !reflect.DeepEqual(v, []byte("world")) {
		t.Errorf("Expected 'world', but got %v", v)
		return false
	}

	// set a bunch of values
	c1.Set(ctx, []byte("howdypartner_1"), []byte("world"), 0)
	c1.Set(ctx, []byte("howdypartner2"), []byte("world"), 0)
	c1.Set(ctx, []byte("howdypartner2"), []byte("world"), 0)
	c1.Set(ctx, []byte("howdypartner2"), []byte("world"), 0)
	c1.Set(ctx, []byte("howdypartner__3"), []byte("world"), 0)
	c1.Set(ctx, []byte("howdypartner______8"), []byte("world"), 0)

	// 0 = never expires
	c1.Set(ctx, []byte("hello"), []byte("world"), 0)
	v = c1.Get(ctx, []byte("hello"))
	if !reflect.DeepEqual(v, []byte("world")) {
		t.Errorf("Expected 'world', but got %v", v)
		return false
	}

	// negative = already expired
	c1.Set(ctx, []byte("hello"), []byte("bugger"), -1)
	v = c1.Get(ctx, []byte("hello"))
	if v != nil {
		t.Errorf("did not expect a value")
		return false
	}

	// caches are seperate
	c1.Set(ctx, []byte("hello"), []byte("world"), 0)
	c2.Set(ctx, []byte("hello"), []byte("universe"), 0)
	v1 := c1.Get(ctx, []byte("hello"))
	v2 := c2.Get(ctx, []byte("hello"))
	if !reflect.DeepEqual(v1, []byte("world")) || !reflect.DeepEqual(v2, []byte("universe")) {
		t.Errorf("Expected 'world' and 'universe', but got %v and %v", v1, v2)
		return false
	}

	return true
}

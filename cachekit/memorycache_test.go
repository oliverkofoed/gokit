package cachekit

import "testing"

func TestMemoryCache(t *testing.T) {
	// create a cache with room for 100kb of data
	c := NewMemoryCache(1024 * 100)

	// regular cache testing
	if !cachetest(c.GetCache("a"), c.GetCache("b"), t) {
		return
	}
}

package cachekit

import (
	"context"
	"io/ioutil"
	"testing"
)

func TestDiskCache2(t *testing.T) {
	ctx := context.Background()

	// get a temporary file
	path, err := ioutil.TempDir("", "diskcache2")
	if err != nil {
		t.Fail()
	}
	defer func() {
		//_ = os.RemoveAll(path)
	}()

	// create a cache with room for 100kb of data
	c, err := NewDiskCache2(ctx, path, 1024*100)
	if err != nil {
		t.Error(err)
		return
	}
	defer c.Close()

	// regular cache testing
	if !cachetest(c.GetCache(ctx, "one"), c.GetCache(ctx, "two"), t) {
		return
	}
}

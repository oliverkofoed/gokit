package cachekit

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestDiskCache(t *testing.T) {
	ctx := context.Background()

	// get a temporary file
	file, err := ioutil.TempFile("", "diskcache")
	if err != nil {
		t.Fail()
	}
	defer func() {
		os.Remove(file.Name())
	}()

	// create a cache with room for 100kb of data
	c, err := NewDiskCache(ctx, file.Name(), 1024*100)
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

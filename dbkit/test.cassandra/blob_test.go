package dbkit_tests

import (
	"context"
	"fmt"
	"testing"

	"bytes"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestBlob(t *testing.T) {
	ctx := context.Background()
	dbURL := "cassandra://127.0.0.1:9042/projectdiscovery?DisableInitialHostLookup=true&IgnorePeerAddr=true"
	db, err := NewDB("cassandra", dbURL)
	testkit.NoError(t, err)

	//insert
	id := []byte{9, 3, 4}
	data := []byte{1, 2, 3}
	original, err := db.Blobs.Insert(ctx, id, 1234, data)
	testkit.NoError(t, err)
	_ = original

	//load
	loaded, err := db.Blobs.Load(ctx, "id=?", id)
	testkit.NoError(t, err)

	//update
	loaded.Data = []byte{9, 9, 9, 9}
	testkit.NoError(t, loaded.Save(ctx))
	loaded2, err := db.Blobs.Load(ctx, "id=?", id)
	testkit.NoError(t, err)
	testkit.Assert(t, bytes.Equal(loaded2.Data, loaded.Data))

	// query first
	loaded3, err := db.Blobs.Query().Where("id=?", id).First(ctx)
	testkit.NoError(t, err)
	testkit.Assert(t, bytes.Equal(loaded3.Data, loaded.Data))

	// query sice
	blobs, err := db.Blobs.Query().Where("id=?", id).Slice(ctx, 100)
	testkit.NoError(t, err)
	testkit.Assert(t, bytes.Equal(blobs[0].Data, loaded.Data))

	// query each
	found := false
	err = db.Blobs.Query().Where("id=?", id).Each(ctx, false, func(b *Blob) error {
		testkit.Assert(t, bytes.Equal(b.Data, loaded.Data))
		found = true
		return nil
	})
	testkit.NoError(t, err)
	testkit.Assert(t, found)

	// delete
	db.Blobs.Delete(ctx, "id=?", id)
	deleted, err := db.Blobs.Load(ctx, "id=?", id)
	testkit.Assert(t, deleted == nil)

	batch := db.NewBatch()
	batch.InsertBlob([]byte{100}, 1, []byte{0, 0, 0, 9})
	batch.InsertBlob([]byte{101}, 2, []byte{0, 0, 0, 10})
	batch.InsertBlob([]byte{102}, 3, []byte{0, 0, 0, 11})
	batch.DeleteBlobByID([]byte{0, 0, 0, 0, 1, 1, 1, 0, 0, 0})
	original.Data = []byte{99}
	batch.SaveBlob(original)
	testkit.NoError(t, batch.Execute(ctx))
	original.Save(ctx)
	//batch.Update()

	b, e := db.Blobs.LoadByID(ctx, []byte{102})
	fmt.Println(b, e)

}

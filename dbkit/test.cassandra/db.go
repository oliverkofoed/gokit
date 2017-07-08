package dbkit_tests

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/gocql/gocql"
	"github.com/oliverkofoed/gokit/logkit"
	"net/url"
)

// DB is the main access point to the database
type DB struct {
	newBatch func() Batch
	Blobs BlobsTable
}

// NewDB creates a new DB pointer to access a database
func NewDB(driverName, dataSourceName string) (*DB, error) {
	switch(driverName){
	
		 
		case "cassandra":
			u, err := url.Parse(dataSourceName)
			if err != nil {
				return nil, err
			}
			cluster := gocql.NewCluster(u.Host)
			if u.Query().Get("DisableInitialHostLookup") == "true" {
				cluster.DisableInitialHostLookup = true
			}
			if u.Query().Get("IgnorePeerAddr") == "true" {
				cluster.IgnorePeerAddr = true
			}
			cluster.Keyspace = u.Path[1:]

			db, err := cluster.CreateSession()
			if err != nil {
				return nil, err
			}

			return &DB{
				newBatch:func() Batch{return &cassandraBatch{db:db, cqlBatch: gocql.NewBatch(gocql.LoggedBatch)}},
				Blobs: BlobsTable{driver: &blobsCassandraDriver{db: db}},
			}, nil
		
	
		default:
			return nil, errors.New("unknown database driver: " + driverName)
	}

}

type Batch interface {
	String() string
	Execute(ctx context.Context) error
	
	
	InsertBlobTTL(ttl int64, id []byte, _type int32, data []byte)
	
	InsertBlob(id []byte, _type int32, data []byte) 
	DeleteBlobByID(id []byte)
	DeleteBlobByIDAndType(id []byte, _type int32)
	SaveBlob(blob *Blob)

}

func (db *DB) NewBatch() Batch{
	return db.newBatch()
}

type loadVarResetable interface{
	resetLoadVars()
}
func (db *DB) NewLoader() *Loader {
	return &Loader{db: db}
}

// Loader makes it easy to load multiple values from multiple tables in one go
type Loader struct {
	db *DB
	idsBlob map[loaderKeyBlob]bool
	valuesBlob map[loaderKeyBlob]*Blob
}

type loaderKeyBlob struct{
	id string 
	_type int32 
}

func (l *Loader) AddBlob(blob *Blob) {
	if l.valuesBlob == nil {
		l.valuesBlob = make(map[loaderKeyBlob]*Blob)
	}
	l.valuesBlob[loaderKeyBlob{id:string(blob.ID),_type:blob.Type}] = blob
}

func (l *Loader) MarkBlobForLoad(id []byte, _type int32) {
	if l.idsBlob == nil {
		l.idsBlob = make(map[loaderKeyBlob]bool)
	}
	l.idsBlob[loaderKeyBlob{string(id),_type}] = true
}

func (l *Loader) GetBlob(id []byte, _type int32) *Blob {
	return l.valuesBlob[loaderKeyBlob{string(id),_type}]
}

func (l *Loader) Load(ctx context.Context) error { 
	if len(l.idsBlob)>0 {
		if l.valuesBlob == nil {
			l.valuesBlob = make(map[loaderKeyBlob]*Blob)
		}
		for id := range l.idsBlob {
			v, err := l.db.Blobs.LoadByIDAndType(ctx,[]byte(id.id),id._type)
			if err != nil {
				return err
			}
			if v != nil {
				l.valuesBlob[loaderKeyBlob{id:string(v.ID),_type:v.Type}] = v
			}
		}
	}

	return nil
}
type cassandraBatch struct {
	db *gocql.Session
	cqlBatch *gocql.Batch 
	saveObjects []loadVarResetable
}

func (b *cassandraBatch) String() string{
	var buf bytes.Buffer 
	for i, stmt := range b.cqlBatch.Entries {
		if i > 0{
			buf.WriteString(";\n")
		}
		cqlString(&buf, stmt.Stmt, stmt.Args)
	}
	return buf.String()
}

func (b *cassandraBatch) Execute(ctx context.Context) error {
	if len(b.cqlBatch.Entries)>0 {
		ctx, done := logkit.Operation(ctx,"cassandra.cql", logkit.Stringer("cql",b))
		defer done()
		b.cqlBatch.DefaultTimestamp(true)
		if err := b.db.ExecuteBatch(b.cqlBatch); err != nil {
			return logkit.Error(ctx, "CQL Error",logkit.Err(err), logkit.Stringer("cql",b))
		}
		for _, obj := range b.saveObjects {
			obj.resetLoadVars()
		}
	}
	return nil
}


func (b *cassandraBatch) SaveBlob(blob *Blob){
	cql, args := getSaveBlobCQL(blob)
	if cql != "" {
		b.cqlBatch.Query(cql, args...)
		b.saveObjects = append(b.saveObjects, blob)
	}
}

func (b *cassandraBatch) InsertBlob(id []byte, _type int32, data []byte){
	b.cqlBatch.Query("insert into Blobs(id, type, data) values (?, ?, ?)", id, _type, data)
}

func (b *cassandraBatch) InsertBlobTTL(ttl int64, id []byte, _type int32, data []byte){
	b.cqlBatch.Query("insert into Blobs(id, type, data) values (?, ?, ?) USING TTL ?", id, _type, data, ttl)
}


func (b *cassandraBatch) DeleteBlobByID(id []byte) {
	b.cqlBatch.Query("delete from Blobs where id=?", id)
}

func (b *cassandraBatch) DeleteBlobByIDAndType(id []byte, _type int32) {
	b.cqlBatch.Query("delete from Blobs where id=? and type=?", id, _type)
}


type cqlStringer struct {
	cql string
	args []interface{}
}

func (c cqlStringer) String() string {
	b := bytes.NewBuffer(nil)
	cqlString(b, c.cql, c.args)
	return b.String()
}

func cqlString(b *bytes.Buffer, cql string, args []interface{}) {
	i := 0
	for _, r := range cql {
		if r == '?' {
			arg := args[i]
			i++
			if s, ok := arg.(string); ok {
				b.WriteString(s)
			} else if s, ok := arg.(fmt.Stringer); ok {
				b.WriteString(s.String())
			} else if arr, ok := arg.([]byte); ok {
				b.WriteString("0x")
				b.WriteString(hex.EncodeToString(arr))
			} else if arr, ok := arg.([][]byte); ok {
				b.WriteString("[")
				for i,val := range arr {
					if i > 0{
						b.WriteString(",")
					}
					b.WriteString(hex.EncodeToString(val))
				}
				b.WriteString("]")
			} else {
				fmt.Fprintf(b, "%v", arg)
			}
		} else {
			b.WriteRune(r)
		}
	}
}
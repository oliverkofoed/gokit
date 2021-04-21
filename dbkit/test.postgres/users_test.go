package dbkit_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/dbkit"
	uuid "github.com/satori/go.uuid"
)

func noerr(err error) {
	if err != nil {
		panic(err)
	}
}

func TestUsersPostgres(t *testing.T) {
	db, err := NewDB("postgres", "host=localhost user=root port=26257 dbname=dbkit_tests sslmode=disable")
	noerr(err)
	testUsers(db, t)
}

func testUsers(db *DB, t *testing.T) {
	ctx := context.Background()
	//c, done := logkit.Operation(nil, "baaah")
	//c.Info("yah")
	//done()

	//email := "oliver@mail.com"
	db.Users.DeleteByEmail(nil, nil)
	db.Users.DeleteByFacebookUserID(nil, dbkit.NullableString("fbid"))

	uid := uuid.NewV4()

	// Create
	u, err := db.Users.Insert(ctx, time.Now(), uid, 0, time.Now(), time.Now(), 0, "Oliver", "abcdef", nil, dbkit.NullableString("fbid"), json.RawMessage("{}"))
	fmt.Println(u)
	noerr(err)

	// Read-Load
	u, err = db.Users.Load(ctx, "ID=$1", u.ID)
	fmt.Println(uid, u)
	//fmt.Println(uid, u.AnotherID)
	noerr(err)

	fmt.Println("...", u.ArbData)

	// Update
	u.DisplayName = "bobby"
	noerr(u.Save(nil))

	// save with no changes
	noerr(u.Save(nil))

	// Load
	_, err = db.Users.Load(ctx, "ID=$1", u.ID)
	noerr(err)
	//fmt.Println(u2)
	//fmt.Println(u)

	// Read-First
	q := db.Users.Query().Where("ID=$1", u.ID)
	_, err = q.First(ctx)
	noerr(err)
	//fmt.Println(u3)

	// Read-Slice
	_, err = q.Slice(ctx, 1)
	noerr(err)
	//fmt.Println("llllllll", uslice)

	// Read-Slice
	noerr(q.Each(ctx, true, func(u *User) error {
		fmt.Println(u)
		return nil
	}))

	// batch test
	batch := db.NewBatch()
	batch.InsertUser(time.Now(), uid, 0, time.Now(), time.Now(), 0, "b1", "abcdef", nil, nil, json.RawMessage("{}"))
	batch.InsertUser(time.Now(), uid, 0, time.Now(), time.Now(), 0, "b2", "abcdef", nil, nil, json.RawMessage("{}"))
	batch.InsertUser(time.Now(), uid, 0, time.Now(), time.Now(), 0, "b3", "abcdef", nil, nil, json.RawMessage("{}"))
	u.DisplayName = "bobbyboy"
	batch.SaveUser(u)
	err = batch.Execute(ctx)
	noerr(err)

	db.Users.Query().Where("display_name = 'bobbyboy'").CountP(ctx)
	// Delete
	// ...
}

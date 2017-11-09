package dbkit_tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/dbkit"
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
	db.Users.DeleteByFacebookUserID(nil, nil)
	db.Users.DeleteByFacebookUserID(nil, dbkit.NullableString(""))

	// Create
	u, err := db.Users.Insert(ctx, time.Now(), 0, time.Now(), time.Now(), 0, "Oliver", "abcdef", nil, dbkit.NullableString(""))
	noerr(err)
	//fmt.Println(u)

	// Read-Load
	u, err = db.Users.Load(ctx, "ID=$1", u.ID)
	noerr(err)
	//fmt.Println(u)

	// Update
	u.DisplayName = "bobby"
	noerr(u.Save(nil))
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

	// Delete
	// ...
}

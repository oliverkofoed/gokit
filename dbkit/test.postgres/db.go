package dbkit_tests

import (
	// db library
	_ "github.com/lib/pq"
	"context"
	"database/sql"
	"errors"
	"time"
)

// DB is the main access point to the database
type DB struct {
	newBatch func() Batch
	Users UsersTable
}

// NewDB creates a new DB pointer to access a database
func NewDB(driverName, dataSourceName string) (*DB, error) {
	switch(driverName){
	
		
		case "postgres":
			db, err := sql.Open(driverName, dataSourceName)
			if err != nil {
				return nil, err
			}

			return &DB{
				Users: UsersTable{driver: &usersPostgresDriver{db: db}},
			}, nil
			
	
		default:
			return nil, errors.New("unknown database driver: " + driverName)
	}

}

type Batch interface {
	String() string
	Execute(ctx context.Context) error
	
	InsertUser(birthdate time.Time, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string) 
	DeleteUserByID(id int64)
	DeleteUserByEmail(email *string)
	DeleteUserByFacebookUserID(facebookUserID *string)
	SaveUser(user *User)

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
	idsUser map[int64]bool
	valuesUser map[int64]*User
}

func (l *Loader) AddUser(user *User) {
	if l.valuesUser == nil {
		l.valuesUser = make(map[int64]*User)
	}
	l.valuesUser[user.ID] = user
}

func (l *Loader) MarkUserForLoad(id int64) {
	if l.idsUser == nil {
		l.idsUser = make(map[int64]bool)
	}
	l.idsUser[id] = true
}

func (l *Loader) GetUser(id int64) *User {
	return l.valuesUser[id]
}

func (l *Loader) Load(ctx context.Context) error { 
	if len(l.idsUser)>0 {
		if l.valuesUser == nil {
			l.valuesUser = make(map[int64]*User)
		}
		for id := range l.idsUser {
			v, err := l.db.Users.LoadByID(ctx,id)
			if err != nil {
				return err
			}
			if v != nil {
				l.valuesUser[v.ID] = v
			}
		}
	}

	return nil
}
type postgresBatch struct {
}

func (b *postgresBatch) String() string{
	panic("not implemented")
}

func (b *postgresBatch) Execute(ctx context.Context) error {
	panic("not implemented")
}


func (b *postgresBatch) SaveUser(user *User){
	panic("not implemented")
}

func (b *postgresBatch) InsertUser(birthdate time.Time, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string){
	panic("not implemented")
}

func (b *postgresBatch) DeleteUserByID(id int64) {
	panic("not implemented")
}

func (b *postgresBatch) DeleteUserByEmail(email *string) {
	panic("not implemented")
}

func (b *postgresBatch) DeleteUserByFacebookUserID(facebookUserID *string) {
	panic("not implemented")
}

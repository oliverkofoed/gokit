package dbkit_tests

import (
	// db library
	_ "github.com/lib/pq"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/oliverkofoed/gokit/logkit"
	"github.com/satori/go.uuid"
	"strconv"
	"time"
)

var Main *DB

// DB is the main access point to the database
type DB struct {
	newBatch func() Batch
	Users *UsersTable
}

// NewDB creates a new DB pointer to access a database
func NewDB(driverName, dataSourceName string) (*DB, error) {
	switch(driverName){
	
		
		case "postgres":
			db, err := sql.Open(driverName, dataSourceName)
			if err != nil {
				return nil, err
			}

			result := &DB{
				newBatch:func() Batch{return &postgresBatch{db:db}},
				Users: &UsersTable{driver: &usersPostgresDriver{db: db}},
			}
			result.Users.driver.(*usersPostgresDriver).table = result.Users
			

			return result, nil
			
	
		default:
			return nil, errors.New("unknown database driver: " + driverName)
	}

}

type Batch interface {
	String() string
	Execute(ctx context.Context) error
	
	
	
	UpsertUser(birthdate time.Time, anotherID uuid.UUID, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string, arbData json.RawMessage)
	
	InsertUser(birthdate time.Time, anotherID uuid.UUID, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string, arbData json.RawMessage) 
	DeleteUserByID(id int64)
	DeleteUserByAnotherID(anotherID uuid.UUID)
	DeleteUserByAnotherIDAndGender(anotherID uuid.UUID, gender int64)
	DeleteUserByEmail(email *string)
	DeleteUserByFacebookUserID(facebookUserID *string)
	DeleteUserByFacebookUserIDAndAvatar(facebookUserID *string, avatar string)
	DeleteUserByAvatar(avatar string)
	DeleteUserByCreated(created time.Time)
	DeleteUserByCreatedAndGender(created time.Time, gender int64)
	DeleteUserByCreatedAndGenderAndBirthdate(created time.Time, gender int64, birthdate time.Time)
	SaveUser(user *User)

}

func (db *DB) NewBatch() Batch{
	return db.newBatch()
}

type loadVarResetable interface{
	resetLoadVars()
}

func panicWrap(err error) error {
	return err
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

func (l *Loader) LoadP(ctx context.Context) {
	if err := l.Load(ctx); err != nil {
		panic(err)
	}
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
	db *sql.DB
	operations []*postgresBatchOperation
	//sql bytes.Buffer
	//statementCount int
	//args []interface{}
}

type postgresBatchOperation struct{
	key string
	sql *bytes.Buffer
	args []interface{}
	saveObject loadVarResetable
}

func (b *postgresBatch) String() string{
	sql := bytes.NewBuffer(nil);
	for i, op := range b.operations {
		if i > 0 {
			sql.WriteString(";\n");
		}
		sql.WriteString(op.sql.String())
	}
	return sql.String()
}

func (b *postgresBatch) ExecuteP(ctx context.Context) {
	err := b.Execute(ctx)
	if err != nil {
		panic(panicWrap(err))
	}
}

func (b *postgresBatch) Execute(ctx context.Context) error {
	if len(b.operations) > 0 {
		ctx, done := logkit.Operation(ctx,"pg.sql", logkit.Stringer("sql",b))
		defer done()
		for _, op := range b.operations {
			sql := op.sql.String()
			if _, err := b.db.ExecContext(ctx, sql, op.args...); err != nil {
				return logkit.Error(ctx, "SQL Error",logkit.Err(err), logkit.String("sql",sql))
			}
			if op.saveObject != nil {
				op.saveObject.resetLoadVars()
			}
		}
	}

	return nil
}

func (b *postgresBatch) ExecuteCockroachDBP(ctx context.Context, noUpdateConflict bool, returningNothing bool) {
	err := b.ExecuteCockroachDB(ctx, noUpdateConflict, returningNothing)
	if err != nil {
		panic(panicWrap(err))
	}
}

func (b *postgresBatch) ExecuteCockroachDB(ctx context.Context, noUpdateConflict bool, returningNothing bool) error {
	if len(b.operations) > 0 {
		ctx, done := logkit.Operation(ctx,"pg.sql", logkit.Stringer("sql",b))
		defer done()
		for _, op := range b.operations {
			if noUpdateConflict {
				op.sql.WriteString(" ON CONFLICT DO NOTHING")
			}
			if returningNothing {
				op.sql.WriteString(" RETURNING NOTHING")
			}
			sql := op.sql.String()
			if _, err := b.db.ExecContext(ctx, sql, op.args...); err != nil {
				return logkit.Error(ctx, "SQL Error",logkit.Err(err), logkit.String("sql",sql))
			}
			if op.saveObject != nil {
				op.saveObject.resetLoadVars()
			}
		}
	}

	return nil
}




func (b *postgresBatch) SaveUser(user *User){
	sql, args := getSaveUserSQL(user, 1)
	if sql != "" {
		sb := bytes.NewBuffer(nil)
		sb.WriteString(sql) //TODO: smarter?
		b.operations = append(b.operations, &postgresBatchOperation{
			sql: sb,
			args: args,
			saveObject: user,
		})
	}
}

func (b *postgresBatch) InsertUser(birthdate time.Time, anotherID uuid.UUID, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string, arbData json.RawMessage){
	operationKey := "insert_User"
	var op *postgresBatchOperation
	for _, o := range b.operations {
		if o.key == operationKey {
			op = o;
			break
		}
	}
	if op == nil {
		sql := bytes.NewBuffer(nil)
		sql.WriteString("insert into Users(birthdate, another_id, gender, created, last_seen, interest, display_name, avatar, email, facebook_user_id, arb_data) values ")

		op = &postgresBatchOperation{
			key: operationKey,
			sql: sql,
		}
		b.operations = append(b.operations, op)
	}

	if len(op.args)> 0 {
		op.sql.WriteString(",")
	}
	op.sql.WriteString("(")
	for i:=0; i!= 11;i++ {
		if i >0 {
			op.sql.WriteString(",")
		}
		op.sql.WriteString("$")
		op.sql.WriteString(strconv.Itoa(1+i+len(op.args)))
	}
	op.sql.WriteString(")")
	op.args = append(op.args, birthdate, anotherID, gender, created, lastSeen, interest, displayName, avatar, email, facebookUserID, arbData)
}

func (b *postgresBatch) UpsertUser(birthdate time.Time, anotherID uuid.UUID, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string, arbData json.RawMessage){
	operationKey := "upsert_User"
	var op *postgresBatchOperation
	for _, o := range b.operations {
		if o.key == operationKey {
			op = o;
			break
		}
	}
	if op == nil {
		sql := bytes.NewBuffer(nil)
		sql.WriteString("upsert into Users(birthdate, another_id, gender, created, last_seen, interest, display_name, avatar, email, facebook_user_id, arb_data) values ")

		op = &postgresBatchOperation{
			key: operationKey,
			sql: sql,
		}
		b.operations = append(b.operations, op)
	}

	if len(op.args)> 0 {
		op.sql.WriteString(",")
	}
	op.sql.WriteString("(")
	for i:=0; i!= 11;i++ {
		if i >0 {
			op.sql.WriteString(",")
		}
		op.sql.WriteString("$")
		op.sql.WriteString(strconv.Itoa(1+i+len(op.args)))
	}
	op.sql.WriteString(")")
	op.args = append(op.args, birthdate, anotherID, gender, created, lastSeen, interest, displayName, avatar, email, facebookUserID, arbData)
}


func (b *postgresBatch) DeleteUserByID(id int64) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where id=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ id },
	})
}

func (b *postgresBatch) DeleteUserByAnotherID(anotherID uuid.UUID) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where another_id=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ anotherID },
	})
}

func (b *postgresBatch) DeleteUserByAnotherIDAndGender(anotherID uuid.UUID, gender int64) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where another_id=$1 and gender=$2")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ anotherID, gender },
	})
}

func (b *postgresBatch) DeleteUserByEmail(email *string) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where email=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ email },
	})
}

func (b *postgresBatch) DeleteUserByFacebookUserID(facebookUserID *string) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where facebook_user_id=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ facebookUserID },
	})
}

func (b *postgresBatch) DeleteUserByFacebookUserIDAndAvatar(facebookUserID *string, avatar string) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where facebook_user_id=$1 and avatar=$2")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ facebookUserID, avatar },
	})
}

func (b *postgresBatch) DeleteUserByAvatar(avatar string) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where avatar=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ avatar },
	})
}

func (b *postgresBatch) DeleteUserByCreated(created time.Time) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where created=$1")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ created },
	})
}

func (b *postgresBatch) DeleteUserByCreatedAndGender(created time.Time, gender int64) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where created=$1 and gender=$2")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ created, gender },
	})
}

func (b *postgresBatch) DeleteUserByCreatedAndGenderAndBirthdate(created time.Time, gender int64, birthdate time.Time) {
	sql := bytes.NewBuffer(nil)
	sql.WriteString("delete from Users where created=$1 and gender=$2 and birthdate=$3")
	b.operations = append(b.operations, &postgresBatchOperation{
		sql: sql,
		args: []interface{}{ created, gender, birthdate },
	})
}


/* // MULTI-statement batchs (not fully supported by cockroachdb yet)

type postgresBatch struct {
	db *sql.DB
	sql bytes.Buffer
	statementCount int
	args []interface{}
	saveObjects []loadVarResetable
}

func (b *postgresBatch) String() string{
	return b.sql.String()
}

func (b *postgresBatch) Execute(ctx context.Context) error {
	if b.statementCount>0 {
		sql := b.sql.String()
		ctx, done := logkit.Operation(ctx,"pg.sql", logkit.String("sql",sql))
		defer done()
		if _, err := b.db.ExecContext(ctx, sql, b.args...); err != nil {
			return logkit.Error(ctx, "SQL Error",logkit.Err(err), logkit.String("sql",sql))
		}
		for _, obj := range b.saveObjects {
			obj.resetLoadVars()
		}
	}

	return nil
}



func (b *postgresBatch) SaveUser(user *User){
	sql, args := getSaveUserSQL(user, len(b.args)+1)
	if sql != "" {
		if b.statementCount > 0 {
			b.sql.WriteString(";\n")
		}
		b.sql.WriteString(sql)
		b.args = append(b.args, args...)
		b.saveObjects = append(b.saveObjects, user)
		b.statementCount++
	}
}

func (b *postgresBatch) InsertUser(birthdate time.Time, anotherID uuid.UUID, gender int64, created time.Time, lastSeen time.Time, interest int64, displayName string, avatar string, email *string, facebookUserID *string, arbData json.RawMessage){
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("insert into Users(birthdate, another_id, gender, created, last_seen, interest, display_name, avatar, email, facebook_user_id, arb_data) values (")
	b.sql.WriteString("$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+2))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+3))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+4))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+5))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+6))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+7))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+8))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+9))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+10))
	b.sql.WriteString(", $")
	b.sql.WriteString(strconv.Itoa(len(b.args)+11))
	b.sql.WriteString(")")
	b.args = append(b.args, birthdate, anotherID, gender, created, lastSeen, interest, displayName, avatar, email, facebookUserID, arbData)
	b.statementCount++
}


func (b *postgresBatch) DeleteUserByID(id int64) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("id=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, id)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByAnotherID(anotherID uuid.UUID) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("another_id=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, anotherID)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByAnotherIDAndGender(anotherID uuid.UUID, gender int64) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("another_id=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.sql.WriteString(" and gender=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+2))
	b.args = append(b.args, anotherID, gender)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByEmail(email *string) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("email=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, email)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByFacebookUserID(facebookUserID *string) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("facebook_user_id=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, facebookUserID)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByFacebookUserIDAndAvatar(facebookUserID *string, avatar string) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("facebook_user_id=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.sql.WriteString(" and avatar=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+2))
	b.args = append(b.args, facebookUserID, avatar)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByAvatar(avatar string) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("avatar=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, avatar)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByCreated(created time.Time) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("created=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, created)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByCreatedAndGender(created time.Time, gender int64) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("created=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.sql.WriteString(" and gender=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+2))
	b.args = append(b.args, created, gender)
	b.statementCount++
}

func (b *postgresBatch) DeleteUserByCreatedAndGenderAndBirthdate(created time.Time, gender int64, birthdate time.Time) {
	if b.statementCount > 0 {
		b.sql.WriteString(";\n")
	}
	b.sql.WriteString("delete from Users where ")
	b.sql.WriteString("created=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+1))
	b.sql.WriteString(" and gender=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+2))
	b.sql.WriteString(" and birthdate=$")
	b.sql.WriteString(strconv.Itoa(len(b.args)+3))
	b.args = append(b.args, created, gender, birthdate)
	b.statementCount++
}


*/
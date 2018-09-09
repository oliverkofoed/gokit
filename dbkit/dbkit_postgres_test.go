package dbkit

import (
	"fmt"
	"net/url"
	"os/exec"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestPostgres(t *testing.T) {
	//Note: this is tested with Cockroachdb and not postgres
	// to replicate, just download cockroachdb and run "./cockroach start --insecure" before running the tests.

	// drop create database
	u, err := url.Parse("postgres://root@127.0.0.1:26257?sslmode=disable")
	testkit.NoError(t, err)
	p, err := OpenPostgres(u)
	testkit.NoError(t, err)
	_, err = p.db.Exec("drop database if exists dbkit_tests")
	testkit.NoError(t, err)
	_, err = p.db.Exec("create database dbkit_tests")
	testkit.NoError(t, err)

	// connect using that database.
	u, err = url.Parse("postgres://root@127.0.0.1:26257/dbkit_tests?sslmode=disable")
	testkit.NoError(t, err)
	p, err = OpenPostgres(u)
	_, err = p.db.Exec(`
		CREATE TABLE users (
			id SERIAL NOT NULL,
			birthdate DATE NOT NULL,
			another_id UUID NOT NULL,
			gender INT NOT NULL,
			created DATE NOT NULL,
			last_seen DATE NOT NULL,
			interest INT NOT NULL,
			display_name TEXT NOT NULL,
			avatar TEXT NOT NULL,
			email TEXT NULL,
			facebook_user_id TEXT NULL,
			CONSTRAINT "primary" PRIMARY KEY (id),
			INDEX interest(avatar),
			INDEX another(another_id),
			INDEX anotherandgender(another_id, gender),
			INDEX interestix( created, gender, birthdate),
			UNIQUE INDEX by_email (email),
			UNIQUE INDEX by_facebook_user_id (facebook_user_id, avatar)
		)
	`)
	testkit.NoError(t, err)

	s, err := p.GetSchema("dbkit_tests", func(msg string, args ...interface{}) {
		fmt.Printf(msg+"\n", args...)
	})
	testkit.NoError(t, err)

	err = s.ReadExtraFieldsFile("test.postgres/extrafields.dbkit", func(msg string, args ...interface{}) {
		fmt.Printf(msg+"\n", args...)
	})
	testkit.NoError(t, err)

	errs := s.Generate("./test.postgres", "postgres")
	if len(errs) > 0 {
		t.Error(errs)
		t.FailNow()
	}

	// run unit test in folder
	c := exec.Command("go", "test")
	c.Dir = "test.postgres"
	out, err := c.CombinedOutput()
	if err != nil {
		t.Error(err, string(out))
		t.FailNow()
	}
}

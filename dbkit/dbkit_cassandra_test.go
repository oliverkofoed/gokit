package dbkit

import (
	"fmt"
	"net/url"
	"os/exec"
	"testing"

	"github.com/gocql/gocql"
	"github.com/oliverkofoed/gokit/testkit"
)

func TestCassandra(t *testing.T) {
	host := "127.0.0.1:9043"

	// drop create schema
	cluster := gocql.NewCluster(host)
	cluster.Keyspace = "system"
	cluster.DisableInitialHostLookup = true
	cluster.IgnorePeerAddr = true
	session, err := cluster.CreateSession()
	testkit.NoError(t, err)

	// drop create scheam
	session.Query("drop keyspace dbkit_tests;").Exec()
	testkit.NoError(t, session.Query("create keyspace dbkit_tests with replication = {'class':'SimpleStrategy','replication_factor':1}").Exec())

	cluster.Keyspace = "dbkit_tests"
	session, err = cluster.CreateSession()
	testkit.NoError(t, err)
	schemaCQL := `CREATE TABLE blobs (
		ID blob,
		Type int,
		Data blob,
		PRIMARY KEY (ID, Type)
	);`
	testkit.NoError(t, session.Query(schemaCQL).Exec())

	u, err := url.Parse("cassandra://" + host + "/dbkit_tests?DisableInitialHostLookup=true&IgnorePeerAddr=true")
	testkit.NoError(t, err)

	p, err := OpenCassandra(u)
	testkit.NoError(t, err)

	s, err := p.GetSchema(u.Path[1:], func(msg string, args ...interface{}) {
		fmt.Printf(msg+"\n", args...)
	})
	testkit.NoError(t, err)

	errs := s.Generate("./test.cassandra", "cassandra")
	if len(errs) > 0 {
		t.Error(errs)
		t.FailNow()
	}

	// run unit test in folder
	c := exec.Command("go", "test")
	c.Dir = "test.cassandra"
	out, err := c.CombinedOutput()
	if err != nil {
		t.Error(err, string(out))
		t.FailNow()
	}
}

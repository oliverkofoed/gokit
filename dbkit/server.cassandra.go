package dbkit

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/gocql/gocql"
	"github.com/jinzhu/inflection"
)

type Cassandra struct {
	db       *gocql.Session
	keyspace string
}

func OpenCassandra(u *url.URL) (*Cassandra, error) {
	cluster := gocql.NewCluster(u.Host)
	cluster.Keyspace = "system"

	cluster.DisableInitialHostLookup = true
	cluster.IgnorePeerAddr = true
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}

	return &Cassandra{db: session, keyspace: u.Path[1:]}, nil
}

func (c *Cassandra) goname(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		switch p {
		case "id":
			parts[i] = strings.ToUpper(parts[i])
			break
		case "ip":
			parts[i] = strings.ToUpper(parts[i])
			break
		case "url":
			parts[i] = strings.ToUpper(parts[i])
			break
		default:
			parts[i] = strings.ToUpper(p[0:1]) + p[1:]
			break
		}
	}
	return strings.Join(parts, "")
}

type cassandraColumn struct {
	Keyspace       string
	Columnfamily   string
	Column         string
	ComponentIndex int
	Index          string
	IndexOptions   string
	IndexType      string
	ColumnType     string
	Validator      string
}
type sortedCassandraColumns []*cassandraColumn

func (s sortedCassandraColumns) Len() int {
	return len(s)
}
func (s sortedCassandraColumns) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s sortedCassandraColumns) Less(i, j int) bool {
	return score(s[i]) < score(s[j])
}
func score(col *cassandraColumn) int {
	if col.ColumnType == "partition_key" {
		return 100 + col.ComponentIndex
	} else if col.ColumnType == "clustering_key" {
		return 1000 + col.ComponentIndex
	}
	return 10000 + col.ComponentIndex
}

func (c *Cassandra) GetSchema(packageName string, log func(msg string, args ...interface{})) (*Schema, error) {
	s := NewSchema(packageName)

	iter := c.db.Query("select * from system.schema_columns").Iter()
	tables := make(map[string][]*cassandraColumn)
	for {
		col := &cassandraColumn{}
		if iter.Scan(&col.Keyspace, &col.Columnfamily, &col.Column, &col.ComponentIndex, &col.Index, &col.IndexOptions, &col.IndexType, &col.ColumnType, &col.Validator) {
			if col.Keyspace == c.keyspace {
				table, found := tables[col.Columnfamily]
				if !found {
					table = make([]*cassandraColumn, 0)
				}
				tables[col.Columnfamily] = append(table, col)
			}
		} else {
			break
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	for table, columns := range tables {
		structName := inflection.Singular(c.goname(table))
		table = strings.ToUpper(table[0:1]) + table[1:]
		t := s.NewTable(table, c.goname(table), structName)
		log("found table: %v (%v)", table, structName)

		sort.Sort(sortedCassandraColumns(columns))

		primaryIndex := []string{}
		for _, col := range columns {
			columnName := col.Column
			goColumnName := c.goname(columnName)
			nullable := false

			if col.ColumnType == "partition_key" || col.ColumnType == "clustering_key" {
				primaryIndex = append(primaryIndex, col.Column)
			}

			if strings.HasPrefix(col.Validator, "org.apache.cassandra.db.marshal.ReversedType(") {
				col.Validator = col.Validator[len("org.apache.cassandra.db.marshal.ReversedType("):]
				col.Validator = col.Validator[:len(col.Validator)-1]
			}
			switch col.Validator {
			case "org.apache.cassandra.db.marshal.Int32TypeT":
				t.AddColumn(columnName, goColumnName, DataTypeInt64, nullable)
			case "org.apache.cassandra.db.marshal.BytesType":
				t.AddColumn(columnName, goColumnName, DataTypeBytes, nullable)
			case "org.apache.cassandra.db.marshal.UTF8Type":
				t.AddColumn(columnName, goColumnName, DataTypeString, nullable)
			case "org.apache.cassandra.db.marshal.TimestampType":
				t.AddColumn(columnName, goColumnName, DataTypeTime, nullable)
			case "org.apache.cassandra.db.marshal.Int32Type":
				t.AddColumn(columnName, goColumnName, DataTypeInt32, nullable)
			case "org.apache.cassandra.db.marshal.TimeUUIDType":
				t.AddColumn(columnName, goColumnName, DataTypeTimeUUID, nullable)
			case "org.apache.cassandra.db.marshal.LongType":
				t.AddColumn(columnName, goColumnName, DataTypeInt64, nullable)
			default:
				return nil, fmt.Errorf("unknown column data type: %v", col.Validator)
			}
		}

		t.SetPrimaryIndex(primaryIndex...)
	}

	return s, nil
}

package dbkit

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	// postgres driver
	"github.com/jinzhu/inflection"
	_ "github.com/lib/pq"
)

type Postgres struct {
	db *sql.DB
}

func OpenPostgres(u *url.URL) (*Postgres, error) {
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return &Postgres{db: db}, nil
}

func (p *Postgres) goname(name string) string {
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

/*func (p *Postgres) caption(name string) string {
	switch name {
	case "id":
		return "ID"
	case "url":
		return "URL"
	default:
		return strings.ToUpper(name[0:1]) + name[1:]
	}
}*/

func (p *Postgres) GetSchema(packageName string, log func(msg string, args ...interface{})) (*Schema, error) {
	s := NewSchema(packageName)

	// 0. get database name
	var dbName *string
	query := "SELECT current_database()"
	errTemplate := "Could not load current database name with query %v. error: %v"
	rows, err := p.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	for rows.Next() {
		if err := rows.Scan(&dbName); err != nil {
			return nil, fmt.Errorf(errTemplate, query, err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	if dbName == nil || *dbName == "" {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	//log("database is %v", *dbName)

	// 1. get tables
	query = "select table_name from information_schema.tables  where table_catalog='" + *dbName + "' and table_schema = 'public'"
	errTemplate = "Could not load tables from " + *dbName + " with query %v. error: %v"
	rows, err = p.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf(errTemplate, query, err)
		}

		structName := inflection.Singular(p.goname(table))
		table = strings.ToUpper(table[0:1]) + table[1:]
		s.NewTable(table, p.goname(table), structName)
		log("found table: %v (%v)", table, structName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}

	// 2. get the columns
	query = "select table_name, column_name, column_default, is_nullable, data_type from information_schema.columns where table_catalog = '" + *dbName + "' and table_schema='public' order by table_name, ordinal_position asc"
	errTemplate = "Could not load columns from " + *dbName + " with query %v. error: %v"
	rows, err = p.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	for rows.Next() {
		var table string
		var columnName string
		var defaultValue *string
		var isNullable string
		var dataType string
		if err := rows.Scan(&table, &columnName, &defaultValue, &isNullable, &dataType); err != nil {
			return nil, fmt.Errorf(errTemplate, query, err)
		}
		nullable := isNullable != "NO"
		table = strings.ToUpper(table[0:1]) + table[1:]
		goColumnName := p.goname(columnName)

		t := s.Tables[table]
		switch strings.ToUpper(dataType) {
		case "INT", "INTEGER", "INT2", "INT4", "INT8", "INT64", "BIGINT":
			if defaultValue != nil && *defaultValue == "unique_rowid()" {
				t.AddColumn(columnName, goColumnName, DataTypeAutoID, nullable)
			} else {
				t.AddColumn(columnName, goColumnName, DataTypeInt64, nullable)
			}
		case "TIMESTAMP", "TIMESTAMPTZ", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITHOUT TIME ZONE":
			t.AddColumn(columnName, goColumnName, DataTypeTime, nullable)
		case "DATE":
			t.AddColumn(columnName, goColumnName, DataTypeDate, nullable)
		case "TEXT", "STRING", "VARCHAR", "CHARACTER VARYING":
			t.AddColumn(columnName, goColumnName, DataTypeString, nullable)
		case "BYTES", "BYTEA":
			t.AddColumn(columnName, goColumnName, DataTypeBytes, nullable)
		case "DOUBLE PRECISION":
			t.AddColumn(columnName, goColumnName, DataTypeFloat64, nullable)
		case "BOOL", "BOOLEAN":
			t.AddColumn(columnName, goColumnName, DataTypeBool, nullable)
		case "UUID":
			t.AddColumn(columnName, goColumnName, DataTypeUUID, nullable)
		case "JSON", "JSONB":
			t.AddColumn(columnName, goColumnName, DataTypeJSON, nullable)
		default:
			return nil, fmt.Errorf("unknown column data type for column: '%s' type: '%v'", columnName, dataType)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}

	// 4. get indexes
	query = "select ixs.tablename, ixs.indexname, a.attname from pg_indexes ixs, pg_class c, pg_attribute a, information_schema.statistics iss where iss.column_name = a.attname AND iss.index_name = ixs.indexname AND iss.table_name = ixs.tablename AND ixs.schemaname = 'public' AND c.oid = ixs.crdb_oid AND a.attrelid = c.oid order by ixs.tablename, ixs.indexname, iss.seq_in_index asc"
	//query = "select ixs.tablename, ixs.indexname, a.attname from pg_indexes ixs, pg_class c, pg_attribute a where ixs.schemaname = 'public' AND c.oid = ixs.crdb_oid AND a.attrelid = c.oid order by ixs.tablename, ixs.indexname, a.attnum"
	//query = "select table_name, constraint_name, column_name  from information_schema.key_column_usage where table_schema = '" + *dbName + "' order by table_name, ordinal_position asc"
	//query = "select table_name, index_name, column_name from information_schema.statistics where index_schema='" + *dbName + "' order by table_name, index_name, seq_in_index asc"
	//query = "select table_name from information_schema.tables  where table_schema='" + *dbName + "'"
	errTemplate = "Could not load indexes from " + *dbName + " with query %v. error: %v"
	rows, err = p.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}
	for rows.Next() {
		var table string
		var indexName string
		var columnName string
		if err := rows.Scan(&table, &indexName, &columnName); err != nil {
			return nil, fmt.Errorf(errTemplate, query, err)
		}

		// find the table
		table = strings.ToUpper(table[0:1]) + table[1:]
		t := s.Tables[table]

		// find/create the index
		var index *Index
		if indexName == "primary" {
			if t.PrimaryIndex == nil {
				t.SetPrimaryIndex()
			}
			index = t.PrimaryIndex
		} else {
			if _, found := t.Indexes[indexName]; !found {
				t.AddIndex(indexName)
			}
			index = t.Indexes[indexName]
		}

		// add the column
		foundCol := t.GetColumn(columnName)
		if foundCol == nil {
			return nil, fmt.Errorf("could not find column %v on table %v", columnName, table)
		}
		index.Columns = append(index.Columns, foundCol)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errTemplate, query, err)
	}

	return s, nil
}

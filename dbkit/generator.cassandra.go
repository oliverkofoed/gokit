package dbkit

import (
	"bytes"
	"fmt"
)

type cassandraGenerator struct {
}

func (p *cassandraGenerator) name() string {
	return "cassandra"
}

func (p *cassandraGenerator) validate(s *Schema) []error {
	errors := make([]error, 0, 0)

	for _, t := range s.Tables {
		for _, col := range t.Columns {
			if col.Type == DataTypeAutoID {
				errors = append(errors, fmt.Errorf("dbkit.Cassandra does not support AutoID columns: %v.%v", t.DBTableName, col.DBName))
			}
		}
		for _, index := range t.Indexes {
			if index.Name != "__primary__" {
				errors = append(errors, fmt.Errorf("dbkit.Cassandra only supports primary indexes, not a primary index: %v.%v", t.DBTableName, index.Name))
			}
		}
	}

	return errors
}

func (p *cassandraGenerator) dbImports(schema *Schema) []string {
	imports := make([]string, 0, 0)
	imports = append(imports, "net/url")
	imports = append(imports, "github.com/gocql/gocql")
	imports = append(imports, "github.com/oliverkofoed/gokit/logkit")
	imports = append(imports, "fmt")
	imports = append(imports, "bytes")
	imports = append(imports, "encoding/hex")

	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if col.Type == DataTypeTime || col.Type == DataTypeDate {
				imports = append(imports, "time")
			}
		}
	}
	return imports
}

func (p *cassandraGenerator) imports(t *Table) []string {
	imports := make([]string, 0, 0)
	imports = append(imports, "context")
	//imports = append(imports, "bytes")
	imports = append(imports, "strconv")
	//imports = append(imports, "database/sql")
	imports = append(imports, "github.com/gocql/gocql")
	imports = append(imports, "github.com/oliverkofoed/gokit/logkit")
	return imports
}

func (p *cassandraGenerator) generate(table *Table, buf *bytes.Buffer) {
	runTemplate("template.cassandra.tmpl", buf, table)
}

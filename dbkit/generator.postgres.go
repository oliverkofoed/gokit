package dbkit

import "bytes"

type postgresGenerator struct {
}

func (p *postgresGenerator) name() string {
	return "postgres"
}

func (p *postgresGenerator) validate(s *Schema) []error {
	errors := make([]error, 0, 0)

	return errors
}

func (p *postgresGenerator) dbImports(schema *Schema) []string {
	imports := make([]string, 0, 0)
	imports = append(imports, "database/sql")
	imports = append(imports, "github.com/oliverkofoed/gokit/logkit")
	imports = append(imports, "bytes")
	imports = append(imports, "strconv")
	hasArray := false
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if col.Type == DataTypeTime || col.Type == DataTypeDate {
				imports = append(imports, "time")
			}
			if col.Type == DataTypeUUID {
				imports = append(imports, "github.com/satori/go.uuid")
			}
			if col.Type == DataTypeJSON {
				imports = append(imports, "encoding/json")
			}
			if col.Type == DataTypeStringArray {
				hasArray = true
			}
		}
	}
	if hasArray {
		imports = append(imports, "github.com/lib/pq")
	} else {
		imports = append(imports, "_github.com/lib/pq")
	}
	return imports
}

func (p *postgresGenerator) imports(t *Table) []string {
	imports := make([]string, 0, 0)
	imports = append(imports, "context")
	imports = append(imports, "bytes")
	imports = append(imports, "strconv")
	imports = append(imports, "fmt")
	imports = append(imports, "database/sql")
	imports = append(imports, "github.com/oliverkofoed/gokit/logkit")
	hasArray := false
	for _, col := range t.Columns {
		if col.Type == DataTypeUUID {
			imports = append(imports, "github.com/satori/go.uuid")
		}
		if col.Type == DataTypeJSON {
			imports = append(imports, "encoding/json")
		}
		if col.Type == DataTypeStringArray {
			hasArray = true
		}
	}
	if hasArray {
		imports = append(imports, "github.com/lib/pq")
	} else {
		imports = append(imports, "_github.com/lib/pq")
	}
	return imports
}

func (p *postgresGenerator) generate(table *Table, buf *bytes.Buffer) {
	runTemplate("template.postgres.tmpl", buf, table)
}

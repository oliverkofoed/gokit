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
	imports = append(imports, "_github.com/lib/pq")
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if col.Type == DataTypeTime || col.Type == DataTypeDate {
				imports = append(imports, "time")
			}
			if col.Type == DataTypeUUID {
				imports = append(imports, "github.com/satori/go.uuid")
			}
		}
	}
	return imports
}

func (p *postgresGenerator) imports(t *Table) []string {
	imports := make([]string, 0, 0)
	imports = append(imports, "context")
	imports = append(imports, "bytes")
	imports = append(imports, "strconv")
	imports = append(imports, "database/sql")
	imports = append(imports, "github.com/oliverkofoed/gokit/logkit")
	imports = append(imports, "_github.com/lib/pq")
	for _, col := range t.Columns {
		if col.Type == DataTypeUUID {
			imports = append(imports, "github.com/satori/go.uuid")
		}
	}
	return imports
}

func (p *postgresGenerator) generate(table *Table, buf *bytes.Buffer) {
	runTemplate("template.postgres.tmpl", buf, table)
}

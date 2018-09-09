package dbkit

import (
	"bytes"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"text/template"
)

//go:generate go-bindata -o templates.go -pkg dbkit template.db.tmpl template.loader.tmpl template.postgres.tmpl template.postgres.db.tmpl template.tableShared.tmpl template.cassandra.tmpl template.cassandra.db.tmpl

// Generate generates table and driver files for the schema
func (s *Schema) Generate(dir string, generatorNames ...string) []error {
	// find generators
	generators, errs := getGenerators(generatorNames)
	if len(errs) > 0 {
		return errs
	}

	// validate
	errs = s.Validate(generators)
	if len(errs) > 0 {
		return errs
	}

	// generate
	//errors := make([]error, 0, 0)
	var buf bytes.Buffer
	for _, table := range s.Tables {
		buf.WriteString("package " + s.PackageName + "\n\n")

		// build a map of imports
		imports := make(map[string]bool)
		imports["bytes"] = true
		imports["strconv"] = true
		extractImports(imports, table.Columns)
		for _, field := range table.ExtraFields {
			if field.Import != "" {
				imports[field.Import] = true
			}
		}
		for _, generator := range generators {
			for _, i := range generator.imports(table) {
				imports[i] = true
			}
		}

		// write imports
		buf.WriteString(importsCode(imports))
		buf.WriteString("\n\n")

		/*buf.WriteString("import (\n")
		importsArr := make([]string, 0, len(imports))
		for path := range imports {
			importsArr = append(importsArr, path)
		}
		sort.Strings(importsArr)
		for _, path := range importsArr {
			if path[0:1] == "_" {
				buf.WriteString("	// db library\n")
				buf.WriteString("	_ \"" + path[1:] + "\"\n")
			} else {
				buf.WriteString("	\"" + path + "\"\n")
			}
		}
		buf.WriteString(")\n\n")*/

		// write shared header
		runTemplate("template.tableShared.tmpl", &buf, table)

		// write section for each generator
		for _, generator := range generators {
			buf.WriteString("\n\n")
			buf.WriteString("// -------- " + generator.name() + " --------\n\n")
			generator.generate(table, &buf)
			break
		}

		destination := path.Join(dir, table.LowerStructName+".table.go")
		ioutil.WriteFile(destination, buf.Bytes(), os.ModePerm)
		buf.Reset()
	}

	// write the DB entry file.
	buf.Reset()
	dbImports := make(map[string]bool)
	dbImports["errors"] = true
	for _, g := range generators {
		for _, path := range g.dbImports(s) {
			dbImports[path] = true
		}
	}
	for path := range s.LoaderImports() {
		dbImports[path] = true
	}

	destination := path.Join(dir, "db.go")
	runTemplate("template.db.tmpl", &buf, struct {
		Schema     *Schema
		Generators []string
		Imports    string
	}{
		Schema:     s,
		Generators: generatorNames,
		Imports:    importsCode(dbImports),
	})
	runTemplate("template.loader.tmpl", &buf, s)

	// write the batch file
	for _, g := range generators {
		runTemplate("template."+g.name()+".db.tmpl", &buf, struct {
			Schema *Schema
			//Generators []string
			Imports string
		}{
			Schema: s,
			//Generators: generatorNames,
			Imports: importsCode(dbImports),
		})

	}
	ioutil.WriteFile(destination, buf.Bytes(), os.ModePerm)
	/*dbImports = make(map[string]bool)
	dbImports["time"] = true
	dbImports["github.com/gocql/gocql"] = true
	buf.Reset()
	destination = path.Join(dir, "batch.go")
	ioutil.WriteFile(destination, buf.Bytes(), os.ModePerm)*/

	return nil
}

func importsCode(imports map[string]bool) string {
	buf := bytes.NewBuffer(nil)
	buf.WriteString("import (\n")
	importsArr := make([]string, 0, len(imports))
	for path := range imports {
		importsArr = append(importsArr, path)
	}
	sort.Strings(importsArr)
	for _, path := range importsArr {
		if path[0:1] == "_" {
			buf.WriteString("	// db library\n")
			buf.WriteString("	_ \"" + path[1:] + "\"\n")
		} else {
			buf.WriteString("	\"" + path + "\"\n")
		}
	}
	buf.WriteString(")")
	return buf.String()
}

func extractImports(imports map[string]bool, columns []*Column) {
	for _, col := range columns {
		if col.Type == DataTypeTime || col.Type == DataTypeDate {
			imports["time"] = true
		}
		if col.Type == DataTypeBytes {
			imports["bytes"] = true
		}
	}
}

func runTemplate(templateName string, buf *bytes.Buffer, data interface{}) {
	// read template from local directory if present., fallback to embedded resource
	templateBytes, err := ioutil.ReadFile(templateName)
	if err != nil {
		templateBytes, err = Asset(templateName)
		if err != nil {
			panic(err)
		}
	}

	// parse template
	t := template.Must(template.New("tmpl").Funcs(template.FuncMap{
		"plusone": func(input int) int { return input + 1 },
		"changed": func(column *Column, currentPrefix, loadedPrefix string) string {
			current := currentPrefix + column.GoName
			loaded := loadedPrefix + column.GoName
			if column.Type == DataTypeBytes { //|| column.Type == DataTypeTimeUUID {
				return "!bytes.Equal(" + currentPrefix + column.GoName + "," + loadedPrefix + column.GoName + ")"
			}
			if column.Type == DataTypeUUID {
				return "!bytes.Equal(" + currentPrefix + column.GoName + ".Bytes()," + loadedPrefix + column.GoName + ".Bytes())"
			}
			if column.Type == DataTypeTime {
				if column.Nullable {
					return current + " != " + loaded + " && !(" + current + " != nil && " + loaded + " != nil && " + current + ".Equal(*" + loaded + "))"
				}
				return "!" + current + ".Equal(" + loaded + ")"
			}

			if column.Nullable {
				return current + " != " + loaded + " && !(" + current + " != nil && " + loaded + " != nil && *" + current + " == *" + loaded + ")"
			}
			return current + " != " + loaded
		},
	}).Parse(string(templateBytes)))

	// execute template
	err = t.Execute(buf, data)
	if err != nil {
		panic(err)
	}
}

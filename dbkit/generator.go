package dbkit

import (
	"bytes"
	"fmt"
)

type generator interface {
	name() string
	validate(schema *Schema) []error
	dbImports(schema *Schema) []string
	imports(table *Table) []string
	generate(table *Table, buf *bytes.Buffer)
}

func getGenerators(names []string) ([]generator, []error) {
	generators := make([]generator, 0, 0)
	errors := make([]error, 0, 0)
	for _, name := range names {
		generator, err := getGenerator(name)
		if err != nil {
			errors = append(errors, err)
		} else {
			generators = append(generators, generator)
		}
	}

	return generators, errors
}

func getGenerator(name string) (generator, error) {
	switch name {
	case "postgres":
		return &postgresGenerator{}, nil
	case "cassandra":
		return &cassandraGenerator{}, nil
	default:
		return nil, fmt.Errorf("Unknown generator: %v", name)
	}
}

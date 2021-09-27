package dbkit

import (
	"fmt"
	"sort"
	"strings"
)

// DataType represents the various data types allowed for table columns
type DataType int

const (
	// DataTypeAutoID maps to an auto increment id or closest matching construct in the database. A table can only have one AutoID columns and it must be the only column in the primary key.
	DataTypeAutoID DataType = iota
	// DataTypeInt64 is a 64bit integer value
	DataTypeInt64
	// DataTypeInt32 is a 32bit integer value
	DataTypeInt32
	// DataTypeFloat64 is a 64bit floating-point number
	DataTypeFloat64
	// DataTypeString is a string value
	DataTypeString
	// DataTypeTime is a time value (date+time)
	DataTypeTime
	// DataTypeDate is a date value (date)
	DataTypeDate
	// DataTypeBytes is a bytes value ([]byte)
	DataTypeBytes
	// DataTypeBool is a boolean value
	DataTypeBool
	// DataTypeTimeUUID is a TimeUUID value
	DataTypeTimeUUID
	// DataTypeTimeUUID is a UUID value
	DataTypeUUID
	// DataTypeJSON is a JSON value
	DataTypeJSON
	// DataTypeStringArray is a string array
	DataTypeStringArray
)

// Schema represents a database schema
type Schema struct {
	PackageName string
	Tables      map[string]*Table
}

// Validate returns a list of validation errors from the schema
func (s *Schema) Validate(generators []generator) []error {
	errors := make([]error, 0, 0)
	for _, t := range s.Tables {
		e := t.Validate(generators)
		errors = append(errors, e...)
	}

	for _, g := range generators {
		errors = append(errors, g.validate(s)...)
	}

	return errors
}

func (s *Schema) SortedTables() []*Table {
	result := make([]*Table, 0, len(s.Tables))
	for _, t := range s.Tables {
		result = append(result, t)
	}
	sort.Sort(byName(result))
	return result
}

type byName []*Table

func (s byName) Len() int {
	return len(s)
}
func (s byName) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s byName) Less(i, j int) bool {
	return s[i].DBTableName < s[j].DBTableName
}

// NewSchema creates a new schema
func NewSchema(packageName string) *Schema {
	return &Schema{
		PackageName: packageName,
		Tables:      make(map[string]*Table),
	}
}

// ExtraField represents an extra go field added to the
// generated struct for a given table
type ExtraField struct {
	Name       string
	GoTypeName string
	Import     string
}

// Table represents a table in a database schema
type Table struct {
	DBTableName string

	GoTableName      string
	GoLowerTableName string

	StructName      string
	LowerStructName string
	Columns         []*Column
	ExtraFields     []*ExtraField
	Indexes         map[string]*Index
	PrimaryIndex    *Index

	Logging bool
}

// Validate returns a list of validation errors from the table
func (t *Table) Validate(generators []generator) []error {
	errors := make([]error, 0, 0)

	// ensure primary index exists with at least one column
	if t.PrimaryIndex == nil {
		errors = append(errors, fmt.Errorf(t.DBTableName+": does not have a primary index"))
	} else if len(t.PrimaryIndex.Columns) == 0 {
		errors = append(errors, fmt.Errorf(t.DBTableName+": the primary index must have at least one column"))
	}

	// only one AutoID column
	autoColCount := 0
	for _, col := range t.Columns {
		if col.Type == DataTypeAutoID {
			autoColCount++
		}
	}
	if (autoColCount == 1 && (t.PrimaryIndex != nil && (len(t.PrimaryIndex.Columns) != 1 || t.PrimaryIndex.Columns[0].Type != DataTypeAutoID))) || autoColCount > 1 {
		errors = append(errors, fmt.Errorf(t.DBTableName+": a table can only have one AutoID column, and it must be the only column in the primary index."))
	}

	return errors
}

// NonAutoIDColumns builds a slice of columns that aren't of type AutoID
func (t *Table) NonAutoIDColumns() []*Column {
	arr := make([]*Column, 0, len(t.Columns))
	for _, c := range t.Columns {
		if c.Type != DataTypeAutoID {
			arr = append(arr, c)
		}
	}
	return arr
}

// AutoIDColumn returns the AutoID column of the table (if any)
func (t *Table) AutoIDColumn() *Column {
	for _, c := range t.Columns {
		if c.Type == DataTypeAutoID {
			return c
		}
	}
	return nil
}

// IndexCombination is a column combinations suitable for index lookup.
type IndexCombination struct {
	Name     string
	FuncArgs string
	CallArgs string
	Columns  []*Column
	Table    Table
}

// IndexCombinations builds a list of column combinations suitable for index lookups.
func (t *Table) IndexCombinations() []*IndexCombination {
	used := make(map[string]bool)
	arr := make([]*IndexCombination, 0, 0)
	for _, key := range sortedKeys(t.Indexes) {
		index := t.Indexes[key]
		for ix := range index.Columns {
			combination := makeIndexCombination(index.Columns[:ix+1], *t)
			if _, found := used[combination.Name]; !found {
				arr = append(arr, combination)
				used[combination.Name] = true
			}
		}
	}

	return arr
}

func sortedKeys(m map[string]*Index) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

func makeIndexCombination(columns []*Column, table Table) *IndexCombination {
	combination := &IndexCombination{Columns: make([]*Column, 0, 0)}
	for _, col := range columns {
		combination.Columns = append(combination.Columns, col)
		combination.Table = table

		// build name
		if combination.Name != "" {
			combination.Name += "And"
		}
		combination.Name += col.GoName

		// build funcargs
		if combination.FuncArgs != "" {
			combination.FuncArgs += ", "
		}
		combination.FuncArgs += col.GoLowerName + " " + col.GoType()

		// build callargs
		if combination.CallArgs != "" {
			combination.CallArgs += ", "
		}
		combination.CallArgs += col.GoLowerName
	}
	return combination
}

// Index represents an index in a database table
type Index struct {
	IsPrimary bool
	Name      string
	Columns   []*Column
	Table     *Table
}

// Column represents a column in a database table
type Column struct {
	DBName      string
	GoName      string
	GoLowerName string
	Type        DataType
	Nullable    bool
}

func (c *Column) IsArray() bool {
	return c.Type == DataTypeStringArray
}

// GoType returns the go typename as a string
func (c *Column) GoType() string {
	prefix := ""
	if c.Nullable {
		prefix = "*"
	}
	switch c.Type {
	case DataTypeAutoID:
		return prefix + "int64"
	case DataTypeInt64:
		return prefix + "int64"
	case DataTypeFloat64:
		return prefix + "float64"
	case DataTypeString:
		return prefix + "string"
	case DataTypeTime:
		return prefix + "time.Time"
	case DataTypeDate:
		return prefix + "time.Time"
	case DataTypeBytes:
		return "[]byte"
	case DataTypeBool:
		return prefix + "bool"
	case DataTypeInt32:
		return prefix + "int32"
	case DataTypeTimeUUID:
		return "gocql.UUID"
	case DataTypeUUID:
		return "uuid.UUID"
	case DataTypeJSON:
		return "json.RawMessage"
	case DataTypeStringArray:
		return "[]string"
	default:
		panic(fmt.Sprintf("don't know go type for: %v", c.Type))
	}
}

// NewTable creates a new table in the schema
func (s *Schema) NewTable(dbTableName string, goTableName string, structName string) *Table {
	t := &Table{}
	t.DBTableName = dbTableName
	t.GoTableName = goTableName
	t.GoLowerTableName = strings.ToLower(goTableName[0:1]) + goTableName[1:]

	t.StructName = structName
	t.LowerStructName = strings.ToLower(structName[0:1]) + structName[1:]

	t.Columns = make([]*Column, 0, 10)
	t.Indexes = make(map[string]*Index)
	s.Tables[dbTableName] = t
	return t
}

// AddColumn adds a colum to the table
func (t *Table) AddColumn(dbName string, goName string, dataType DataType, nullable bool) {
	goNameLower := strings.ToLower(goName[0:1]) + goName[1:]
	if goNameLower == "iD" {
		goNameLower = "id"
	}
	if goNameLower == "oS" {
		goNameLower = "os"
	}
	if goNameLower == "type" {
		goNameLower = "_type"
	}

	t.Columns = append(t.Columns, &Column{
		DBName:      dbName,
		GoName:      goName,
		GoLowerName: goNameLower,
		Type:        dataType,
		Nullable:    nullable,
	})
}

// SetPrimaryIndex sets the primary index on the table
func (t *Table) SetPrimaryIndex(columns ...string) {
	t.PrimaryIndex = t.AddIndex("__primary__", columns...)
}

// AddIndex adds the index to the table
func (t *Table) AddIndex(name string, columns ...string) *Index {
	index := &Index{Table: t}
	index.Name = name
	index.Columns = make([]*Column, 0, 0)
	for _, n := range columns {
		foundCol := t.GetColumn(n)
		if foundCol == nil {
			panic("could not find column " + n + " in table " + t.DBTableName)
		}
		index.Columns = append(index.Columns, foundCol)
	}
	t.Indexes[index.Name] = index
	return index
}

// GetColumn gets the specified column
func (t *Table) GetColumn(name string) *Column {
	for _, pc := range t.Columns {
		if pc.DBName == name {
			return pc
		}
	}
	return nil
}

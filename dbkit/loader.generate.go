package dbkit

import (
	"bytes"
)

func (s *Schema) LoaderImports() map[string]bool {
	imports := make(map[string]bool)
	for _, table := range s.Tables {
		extractImports(imports, table.PrimaryIndex.Columns)
	}
	imports["context"] = true
	return imports
}
func (i *Index) LoaderKeyGoType() string {
	if len(i.Columns) > 1 {
		return "loaderKey" + i.Table.StructName
	}
	if i.Columns[0].GoType() == "[]byte" {
		return "string"
	}
	return i.Columns[0].GoType()
}

func (i *Index) LoaderKeyFromStruct(variable string) string {
	if len(i.Columns) == 1 {
		return strWrap(i.Columns[0].GoType(), variable+"."+i.Columns[0].GoName)
	}
	buf := bytes.NewBuffer(nil)
	buf.WriteString(i.LoaderKeyGoType())
	buf.WriteString("{")
	for i, c := range i.Columns {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(c.GoLowerName)
		buf.WriteString(":")
		buf.WriteString(strWrap(c.GoType(), variable+"."+c.GoName))
	}
	buf.WriteString("}")
	return buf.String()
}

func (i *Index) LoaderKeyFuncArgs() string {
	buf := bytes.NewBuffer(nil)
	for i, c := range i.Columns {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(c.GoLowerName)
		buf.WriteString(" ")
		buf.WriteString(c.GoType())
	}
	return buf.String()
}

func (i *Index) LoaderKeyFuncValue() string {
	if len(i.Columns) == 1 {
		return strWrap(i.Columns[0].GoType(), i.Columns[0].GoLowerName)
	}

	buf := bytes.NewBuffer(nil)
	buf.WriteString(i.LoaderKeyGoType())
	buf.WriteString("{")
	for i, c := range i.Columns {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(strWrap(c.GoType(), c.GoLowerName))
	}
	buf.WriteString("}")
	return buf.String()
}

func (i *Index) LoaderKeyUnpack(variable string) string {
	buf := bytes.NewBuffer(nil)
	for i, c := range i.Columns {
		if i > 0 {
			buf.WriteString("And")
		}
		buf.WriteString(c.GoName)
	}
	buf.WriteString("(ctx,")
	if len(i.Columns) == 1 {
		buf.WriteString(strUnwrap(i.Columns[0].GoType(), variable))
	} else {
		for i, c := range i.Columns {
			if i > 0 {
				buf.WriteString(",")
			}
			buf.WriteString(strUnwrap(c.GoType(), variable+"."+c.GoLowerName))
		}
	}
	buf.WriteString(")")
	return buf.String()
}

func strWrap(goType string, code string) string {
	if goType == "[]byte" {
		return "string(" + code + ")"
	}
	return code
}

func strUnwrap(goType string, code string) string {
	if goType == "[]byte" {
		return "[]byte(" + code + ")"
	}
	return code
}

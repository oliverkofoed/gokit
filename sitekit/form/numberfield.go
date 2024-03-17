package form

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strconv"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type NumberField struct {
	// configurable by user
	Name        string
	Caption     string
	Required    bool
	Description string
	Min         *int64
	Max         *int64
	Placeholder string
	Attributes  map[string]string

	// readable value
	Error string
	Value *int64
}

func (t *NumberField) ValueNoNil() int64 {
	if t.Value == nil {
		return 0
	}
	return *t.Value
}

func (t *NumberField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	t.Error = ""

	value := c.PostForm.String(t.Name, "")
	// parse value
	if t.Required && value == "" {
		t.Error = texts.ErrorRequired
		return
	}

	if value == "" {
		t.Value = nil
	} else {
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Error = err.Error()
			return
		}

		if t.Min != nil && v < *t.Min {
			t.Error = fmt.Sprintf(texts.ErrorValueBelowMin, *t.Min)
			return
		}

		t.Value = &v
	}
}

func (t *NumberField) Render(buffer *bytes.Buffer) {
	buffer.WriteString("<input")
	addType(buffer, "number")
	buffer.WriteString(" id=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\" name=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\"")
	if t.Placeholder != "" {
		buffer.WriteString(" placeholder=\"")
		buffer.WriteString(t.Placeholder)
		buffer.WriteString("\"")
	}

	if t.Attributes != nil {
		for k, v := range t.Attributes {
			buffer.WriteString(" ")
			buffer.WriteString(k)
			buffer.WriteString("=\"")
			buffer.WriteString(html.EscapeString(v))
			buffer.WriteString("\"")
		}
	}

	if t.Value != nil {
		buffer.WriteString(" value=\"")
		buffer.WriteString(fmt.Sprintf("%v", *t.Value))
		buffer.WriteString("\"")
	}
	buffer.WriteString(">")
}

func (t *NumberField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *NumberField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *NumberField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *NumberField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

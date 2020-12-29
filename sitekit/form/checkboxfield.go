package form

import (
	"bytes"
	"html"
	"html/template"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type CheckboxField struct {
	// configurable by user
	Name        string
	Caption     string
	Required    bool
	Description string
	Attributes  map[string]string

	// readable value
	Error string
	Value bool
}

func (t *CheckboxField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	v := strings.ToLower(c.Request.Form.Get(t.Name))
	t.Error = ""
	t.Value = v == "on" || v == "true"
}

func (t *CheckboxField) Render(buffer *bytes.Buffer) {
	buffer.WriteString("<input type=\"checkbox\"")
	buffer.WriteString(" id=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\" name=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\"")
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	if t.Value {
		t.Attributes["checked"] = "checked"
	}
	for k, v := range t.Attributes {
		buffer.WriteString(" ")
		buffer.WriteString(k)
		buffer.WriteString("=\"")
		buffer.WriteString(html.EscapeString(v))
		buffer.WriteString("\"")
	}
	buffer.WriteString(">")
}

func (t *CheckboxField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *CheckboxField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *CheckboxField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *CheckboxField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

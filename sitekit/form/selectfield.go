package form

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type SelectField struct {
	// configurable by user
	Name        string
	Caption     string
	Required    bool
	Description string
	Options     []*Option
	Attributes  map[string]string

	// readable value
	Error string
	Value interface{}
}

type Option struct {
	Caption string
	Name    string
	Value   interface{}
}

func (t *SelectField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	valueStr := c.PostForm.String(t.Name, "")
	t.Error = ""

	if t.Required && valueStr == "" {
		t.Error = texts.ErrorRequired
		return
	}

	if valueStr != "" {
		found := false
		ensureNames(t.Options)
		for _, option := range t.Options {
			if option.Name == valueStr {
				t.Value = option.Value
				found = true
				break
			}
		}

		if t.Required && !found {
			t.Error = texts.ErrorRequired
			return
		}
	}
}

func (t *SelectField) Render(buffer *bytes.Buffer) {
	buffer.WriteString("<select")

	buffer.WriteString(" id=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\" name=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\"")

	if t.Attributes != nil {
		for k, v := range t.Attributes {
			buffer.WriteString(" ")
			buffer.WriteString(k)
			buffer.WriteString("=\"")
			buffer.WriteString(html.EscapeString(v))
			buffer.WriteString("\"")
		}
	}
	buffer.WriteString(">")
	ensureNames(t.Options)
	for _, option := range t.Options {
		buffer.WriteString("<option value=\"")
		buffer.WriteString(option.Name)
		buffer.WriteString("\"")
		if option.Value == t.Value {
			buffer.WriteString(" selected=\"selected\"")
		}
		buffer.WriteString(">")
		buffer.WriteString(option.Caption)
		buffer.WriteString("</option>")
	}
	buffer.WriteString("</select>")
}

var optionNameRegex = regexp.MustCompile("[^a-zA-Z0-9]")

func ensureNames(options []*Option) {
	for i, option := range options {
		if option.Name == "" {
			option.Name = fmt.Sprintf("%v_%v", optionNameRegex.ReplaceAllString(option.Caption, ""), i)
		}
	}
}

func (t *SelectField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *SelectField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *SelectField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *SelectField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

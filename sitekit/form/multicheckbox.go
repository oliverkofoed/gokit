package form

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type MultiCheckbox struct {
	// configurable by user
	Name        string
	Caption     string
	Description string
	Options     []*Option
	Attributes  map[string]string

	// readable value
	Error string
	Value interface{}
}

func (t *MultiCheckbox) Bind(c *web.Context, texts *Text) {
	ensureNames(t.Options)
	t.Error = ""
	values := make([]*Option, 0)
	for _, option := range t.Options {
		if c.PostForm.Bool(option.Name, false) {
			values = append(values, option)
		}
	}
	t.Value = values
}

func (t *MultiCheckbox) Render(buffer *bytes.Buffer) {
	ensureNames(t.Options)

	var current map[string]bool
	if m, ok := t.Value.(string); ok {
		parts := strings.Split(m, ",")
		current = make(map[string]bool)
		for _, part := range parts {
			current[strings.TrimSpace(part)] = true
		}
	} else if m, ok := t.Value.([]*Option); ok {
		current = make(map[string]bool)
		for _, option := range m {
			current[option.Name] = true
		}
	} else {
		panic(fmt.Errorf("Unknown value type for multicheckbox: %v", t.Value))
	}

	for _, option := range t.Options {
		//id := optionNameRegex.ReplaceAllString(t.Name, "") + "_" + optionNameRegex.ReplaceAllString(option, "")
		buffer.WriteString("<span class=\"checkbox\">")
		buffer.WriteString("<input type=\"checkbox\"")
		buffer.WriteString(" id=\"")
		buffer.WriteString(option.Name)
		buffer.WriteString("\" name=\"")
		buffer.WriteString(option.Name)
		buffer.WriteString("\"")
		if enabled, found := current[option.Name]; found && enabled {
			buffer.WriteString(" checked=\"checked\"")
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
		buffer.WriteString(">")
		buffer.WriteString("<label class=\"checkboxlabel\" for=\"")
		buffer.WriteString(option.Name)
		buffer.WriteString("\">")
		buffer.WriteString(option.Caption)
		buffer.WriteString("</label>")
		buffer.WriteString("</span>")
	}
}

func (t *MultiCheckbox) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *MultiCheckbox) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *MultiCheckbox) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *MultiCheckbox) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

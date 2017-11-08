package form

import (
	"bytes"
	"html"
	"html/template"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type InputType int

const (
	InputTypeText InputType = iota
	InputTypeTextArea
	InputTypeHidden
	InputTypePassword
	InputTypeEmail
	InputTypeWebsite
)

type InputField struct {
	// configurable by user
	Type        InputType
	Name        string
	Caption     string
	Required    bool
	Description string
	MaxLength   int
	MinLength   int
	Regexp      *regexp.Regexp
	RegexpError string
	Placeholder string
	Attributes  map[string]string

	// readable value
	Error string
	Value string
}

func (t *InputField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	t.Value = c.PostForm.String(t.Name, "")
	t.Error = ""

	if t.Required && t.Value == "" {
		t.Error = texts.ErrorRequired
		return
	}

	if t.Value != "" {
		if t.MaxLength > 0 && len(t.Value) > t.MaxLength {
			t.Error = texts.ErrorTooLong
			return
		}

		if len(t.Value) < t.MinLength {
			t.Error = texts.ErrorTooShort
			return
		}

		switch t.Type {
		case InputTypeEmail:
			adr, err := mail.ParseAddress(t.Value)
			if err != nil {
				t.Error = texts.ErrorInvalidEmail
				return
			}
			t.Value = adr.Address
		case InputTypeWebsite:
			adr, err := url.Parse(t.Value)
			if err != nil || (adr.Scheme != "http" && adr.Scheme != "https") || adr.Host == "" || strings.Contains(adr.Host, ".") == false {
				adr, err = url.Parse("http://" + t.Value)
				if err != nil || (adr.Scheme != "http" && adr.Scheme != "https") || adr.Host == "" || strings.Contains(adr.Host, ".") == false {
					t.Error = texts.ErrorInvalidWebsite
					return
				}
			}
			t.Value = adr.String()
		}

		// matching for built in types.
		if t.Regexp != nil {
			if !t.Regexp.MatchString(t.Value) {
				t.Error = t.RegexpError
				if t.Error == "" {
					t.Error = "Error: No RegexpError message given. The text did not match the Regexp."
				}
			}
		}
	}

}

func (t *InputField) Render(buffer *bytes.Buffer) {
	if t.Type == InputTypeTextArea {
		buffer.WriteString("<textarea")
	} else {
		buffer.WriteString("<input")
	}

	switch t.Type {
	case InputTypeText:
		addType(buffer, "text")
	case InputTypeHidden:
		addType(buffer, "hidden")
	case InputTypePassword:
		addType(buffer, "password")
	case InputTypeEmail:
		addType(buffer, "email")
	case InputTypeWebsite:
		addType(buffer, "text")
	default:
		addType(buffer, "text")
	}

	buffer.WriteString(" id=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\" name=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\"")
	if t.MaxLength > 0 {
		buffer.WriteString(" maxlength=\"")
		buffer.WriteString(string(t.MaxLength))
		buffer.WriteString("\"")
	}
	if t.Placeholder != "" {
		buffer.WriteString(" placeholder=\"")
		buffer.WriteString(html.EscapeString(t.Placeholder))
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

	if t.Type == InputTypeTextArea {
		buffer.WriteString(">")
		buffer.WriteString(html.EscapeString(t.Value))
		buffer.WriteString("</textarea>")
	} else {
		buffer.WriteString(" value=\"")
		buffer.WriteString(html.EscapeString(t.Value))
		buffer.WriteString("\">")
	}
}

func addType(buffer *bytes.Buffer, t string) {
	buffer.WriteString(" type=\"")
	buffer.WriteString(t)
	buffer.WriteString("\"")
}

func (t *InputField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *InputField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *InputField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *InputField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

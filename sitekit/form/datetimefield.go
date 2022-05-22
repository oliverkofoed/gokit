package form

import (
	"bytes"
	"html"
	"html/template"
	"time"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type DateTimeInputType int

const (
	DateTimeInputDateTimeLocal DateTimeInputType = iota
	DateTimeInputDate
	DateTimeInputTime
)

type DateTimeField struct {
	// configurable by user
	Type        DateTimeInputType
	Name        string
	Caption     string
	Required    bool
	Description string
	MinDate     *time.Time
	MaxDate     *time.Time
	Placeholder *time.Time
	Attributes  map[string]string

	// readable value
	Error string
	Value *time.Time
}

func (t *DateTimeField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	t.Error = ""

	// parse value
	value := c.PostForm.String(t.Name, "")
	if t.Required && value == "" {
		t.Error = texts.ErrorRequired
		return
	}

	var err error
	var dt time.Time
	switch t.Type {
	case DateTimeInputDateTimeLocal:
		dt, err = time.Parse("2006-01-02T15:04", value)
	case DateTimeInputDate:
		dt, err = time.Parse("2006-01-02", value)
	case DateTimeInputTime:
		dt, err = time.Parse("15:04", value)
	default:
		dt, err = time.Parse("2006-01-02T15:04", value)
	}
	if err != nil {
		t.Error = err.Error()
		return
	}
	t.Value = &dt

	//t.Value = c.PostForm.String(t.Name, "")

	/*
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
			case InputTypeDate:
				if _, err := time.Parse("2006-01-02", t.Value); err != nil {
					t.Error = texts.ErrorInvalidDate
				}
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
		}*/

}

func (t *DateTimeField) Render(buffer *bytes.Buffer) {
	buffer.WriteString("<input")

	switch t.Type {
	case DateTimeInputDateTimeLocal:
		addType(buffer, "datetime-local")
	case DateTimeInputDate:
		addType(buffer, "date")
	case DateTimeInputTime:
		addType(buffer, "time")
	default:
		addType(buffer, "date")
	}

	buffer.WriteString(" id=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\" name=\"")
	buffer.WriteString(t.Name)
	buffer.WriteString("\"")
	if t.Placeholder != nil {
		buffer.WriteString(" placeholder=\"")
		buffer.WriteString(timeString(*t.Placeholder, t.Type))
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
		buffer.WriteString(timeString(*t.Value, t.Type))
		buffer.WriteString("\"")
	}
	buffer.WriteString(">")
}

func timeString(v time.Time, formatting DateTimeInputType) string {
	switch formatting {
	case DateTimeInputDateTimeLocal:
		return v.Format("2006-01-02T15:04")
	case DateTimeInputDate:
		return v.Format("2006-01-02")
	case DateTimeInputTime:
		return v.Format("15:04")
	default:
		return v.Format("2006-01-02T15:04")
	}
}

func (t *DateTimeField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *DateTimeField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *DateTimeField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *DateTimeField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

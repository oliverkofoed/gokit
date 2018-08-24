package form

import (
	"bytes"
	"html"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

/*type FileFieldCache interface {
	Set(key string, value io.Reader)
	Get(key string) io.ReadSeeker
}*/

type FileField struct {
	// configurable by user
	Name        string
	Caption     string
	Required    bool
	Description string
	Attributes  map[string]string

	// readable value
	Error string
	Value *UploadedFile
}

type UploadedFile struct {
	Filename    string
	ContentType string
	File        io.ReadSeeker
}

func (u *UploadedFile) Bytes() []byte {
	u.File.Seek(0, io.SeekStart)
	content, err := ioutil.ReadAll(u.File)
	if err != nil {
		panic(err)
	}
	u.File.Seek(0, io.SeekStart)
	return content
}

func (t *FileField) Bind(c *web.Context, texts *Text) {
	if texts == nil {
		texts = &DefaultText
	}

	t.Error = ""

	file, header, err := c.Request.FormFile(t.Name)

	if err != nil {
		if err == http.ErrMissingFile {
			if t.Required {
				t.Error = texts.ErrorRequired
				return
			}
		} else {
			t.Error = err.Error()
			return
		}
	}

	if file != nil {
		contentType := "application/octet-stream"
		if header != nil && header.Header != nil {
			if c := header.Header.Get("Content-Type"); c != "" {
				contentType = c
			}
		}

		t.Value = &UploadedFile{
			Filename:    header.Filename,
			ContentType: contentType,
			File:        file,
		}
	}
}

func (t *FileField) Render(buffer *bytes.Buffer) {
	buffer.WriteString("<input type=\"file\"")

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
}

func (t *FileField) SetAttribute(key, value string) {
	if t.Attributes == nil {
		t.Attributes = make(map[string]string)
	}
	t.Attributes[key] = value
}

// -------------------------------------

func (t *FileField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *FileField) RowHTML() template.HTML {
	return renderRowHTML(t)
}

func (t *FileField) GetRenderDetails() (name, desc, caption, err string) {
	return t.Name, t.Description, t.Caption, t.Error
}

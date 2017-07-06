package form

import (
	"bytes"
	"html/template"
	"time"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

var XSRFTokenValidTime = time.Hour
var XSRFTokenGenerator = func(c *web.Context, seed int64) string {
	return "" //fmt.Sprintf("%v", seed)
}

func GetXSRFToken(c *web.Context) string {
	return XSRFTokenGenerator(c, getXSRFSeed(time.Now().Unix(), 0))
}

func IsXSRFTokenValid(c *web.Context, token string) bool {
	now := time.Now().Unix()
	if token != XSRFTokenGenerator(c, getXSRFSeed(now, 0)) {
		if token != XSRFTokenGenerator(c, getXSRFSeed(now, -1)) {
			if token != XSRFTokenGenerator(c, getXSRFSeed(now, 1)) {
				return false
			}
		}
	}
	return true
}

type XSRFField struct {
	C     *web.Context
	value string
	bound bool
	Error string
}

func (t *XSRFField) Bind(c *web.Context, texts *Text) {
	t.value = c.PostForm.String("xsrfprotection", "")
	t.bound = true

	if !IsXSRFTokenValid(t.C, t.value) {
		t.Error = "Invalid Token"
	}
}

func (t *XSRFField) Render(buffer *bytes.Buffer) {
	if !t.bound {
		t.value = GetXSRFToken(t.C)
	}
	buffer.WriteString("<input type=\"hidden\" name=\"xsrfprotection\" value=\"")
	buffer.WriteString(t.value)
	buffer.WriteString("\">")
}

func getXSRFSeed(unixTime int64, offset int) int64 {
	return int64(unixTime / int64(XSRFTokenValidTime))
}

// -------------------------------------

func (t *XSRFField) HTML() template.HTML {
	var buffer bytes.Buffer
	t.Render(&buffer)
	return template.HTML(buffer.String())
}

func (t *XSRFField) RowHTML() template.HTML {
	return t.HTML()
}

func (t *XSRFField) GetRenderDetails() (name, desc, caption, err string) {
	return "", "", "", t.Error
}

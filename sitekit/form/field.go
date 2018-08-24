package form

import (
	"bytes"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type Field interface {
	Bind(c *web.Context, texts *Text)
	SetAttribute(name, value string)
	Render(buffer *bytes.Buffer)
	GetRenderDetails() (name, desc, caption, err string)
}

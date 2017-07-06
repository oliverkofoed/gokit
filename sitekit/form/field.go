package form

import (
	"bytes"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type Field interface {
	Bind(c *web.Context, texts *Text)
	Render(buffer *bytes.Buffer)
	GetRenderDetails() (err, desc, name, caption string)
}

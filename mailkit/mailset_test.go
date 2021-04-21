package mailkit_test

import (
	"testing"

	"github.com/oliverkofoed/gokit/dev"
	"github.com/oliverkofoed/gokit/mailkit/postmark"
	"github.com/oliverkofoed/gokit/testkit"
)

func TestMailkit(t *testing.T) {
	p := postmark.New()
	mail, err := p.GenerateMail("somebody@somehwere", "destination@there.com", "this is my email", p.Master("product", "preheader", "", 0, 0, "", []string{"bob"}))
	testkit.NoError(t, err)
	dev.JSON(mail)
}

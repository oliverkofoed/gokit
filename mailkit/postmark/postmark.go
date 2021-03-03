package postmark

import (
	"embed"
	"fmt"
	"time"

	"github.com/oliverkofoed/gokit/mailkit"
)

//go:embed *.html
//go:embed *.txt
var files embed.FS

type Postmark struct {
	*mailkit.Mailset
}

func New() *Postmark {
	mailset := mailkit.NewMailSet()
	mailset.RegisterBlocksFromFS(files)
	mailset.RegisterBlock("inlinecode", "<b>{{.Args.Code}}</b>", "Code: {{.Args.Code}}")
	mailset.RegisterBlock("h1", `<h1 style="margin-top: 0; color: #333333; font-size: 22px; font-weight: bold; text-align: left;" align="left">{{.Args.Text}}</h1>`, "* {{.Args.Text}} *\n\n")
	mailset.RegisterBlock("p", `<p style="font-size: 16px; line-height: 1.625; color: #333; margin: .4em 0 1.1875em;">{{.Args.Text}}</p>`, "{{.Args.Text}}\n\n")
	mailset.RegisterBlock("smallp", `<p class="sub" style="font-size: 13px; line-height: 1.625; color: #333; margin: .4em 0 1.1875em;">{{.Args.Text}}</p>`, "{{.Args.Text}}\n\n")
	mailset.RegisterBlock("br", `<br>`, "\n")
	return &Postmark{
		mailset,
	}
}

func (p *Postmark) Master(productname string, preheader string, address []string) *mailkit.Block {
	return p.Mailset.CreateBlock("master", struct {
		Preheader     string
		ProductName   string
		Address       []string
		CopyrightYear string
	}{
		ProductName:   productname,
		Preheader:     preheader,
		Address:       address,
		CopyrightYear: fmt.Sprintf("%v", time.Now().Year()),
	})
}

func (p *Postmark) H1(text string) *mailkit.Block {
	return p.Mailset.CreateBlock("h1", struct{ Text string }{Text: text})
}

func (p *Postmark) P(text string) *mailkit.Block {
	return p.Mailset.CreateBlock("p", struct{ Text string }{Text: text})
}

func (p *Postmark) SmallP(text string) *mailkit.Block {
	return p.Mailset.CreateBlock("smallp", struct{ Text string }{Text: text})
}

func (p *Postmark) ButtonRed(caption string, link string) *mailkit.Block {
	return p.Mailset.CreateBlock("button-red", struct {
		Caption string
		Link    string
	}{Caption: caption, Link: link})
}

func (p *Postmark) ButtonGreen(caption string, link string) *mailkit.Block {
	return p.Mailset.CreateBlock("button-green", struct {
		Caption string
		Link    string
	}{Caption: caption, Link: link})
}

func (p *Postmark) ButtonBlue(caption string, link string) *mailkit.Block {
	return p.Mailset.CreateBlock("button-blue", struct {
		Caption string
		Link    string
	}{Caption: caption, Link: link})
}

func (p *Postmark) BR() *mailkit.Block {
	return p.Mailset.CreateBlock("br", struct{}{})
}

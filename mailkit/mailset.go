package mailkit

import (
	"bytes"
	"fmt"
	"html/template"
	htmltemplate "html/template"
	"io/fs"
	"path/filepath"
	texttemplate "text/template"
)

type Block struct {
	template *blockTemplate
	args     interface{}
}

type blockTemplate struct {
	html *htmltemplate.Template
	text *texttemplate.Template
}

type Mailset struct {
	blocks map[string]*blockTemplate
}

func NewMailSet() *Mailset {
	return &Mailset{
		blocks: make(map[string]*blockTemplate),
	}
}

func (m *Mailset) RegisterBlocksFromFS(filesystem fs.FS) {
	files, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		panic(err)
	}

	mapping := make(map[string]map[string]string)
	for _, f := range files {
		name := f.Name()
		t := filepath.Ext(name)
		b := filepath.Base(name)
		b = b[0 : len(b)-len(t)]

		df, found := mapping[b]
		if !found {
			df = make(map[string]string)
			mapping[b] = df
		}
		df[t[1:]] = f.Name()
	}

	for name, files := range mapping {
		htmlTemplate := ""
		textTemplate := ""
		if path, found := files["html"]; found {
			buf, err := fs.ReadFile(filesystem, path)
			if err != nil {
				panic(err)
			}
			htmlTemplate = string(buf)
		}
		if path, found := files["txt"]; found {
			buf, err := fs.ReadFile(filesystem, path)
			if err != nil {
				panic(err)
			}
			textTemplate = string(buf)
		}
		m.RegisterBlock(name, htmlTemplate, textTemplate)
	}
}

func (m *Mailset) RegisterBlock(name string, htmlTemplate string, textTemplate string) {
	result := &blockTemplate{}
	var err error

	result.html, err = htmltemplate.New(name + ".html").Parse(htmlTemplate)
	if err != nil {
		panic(err)
	}

	result.text, err = texttemplate.New(name + ".txt").Parse(textTemplate)
	if err != nil {
		panic(err)
	}

	m.blocks[name] = result
}

func (m *Mailset) CreateBlock(name string, args interface{}) *Block {
	template, found := m.blocks[name]
	if !found {
		panic(fmt.Errorf("Could not find block: %v", name))
	}
	return &Block{
		template: template,
		args:     args,
	}
}

func (m *Mailset) RenderHTML(master *Block, blocks ...*Block) (string, error) {
	var buf bytes.Buffer

	// render all the content blocks
	renderedBlocks := make([]template.HTML, 0, len(blocks))
	for _, b := range blocks {
		b.template.html.Execute(&buf, struct {
			Args interface{}
		}{
			Args: b.args,
		})

		renderedBlocks = append(renderedBlocks, template.HTML(buf.Bytes()))
		buf.Reset()
	}

	// render master, replacing content into it.
	buf.Reset()
	master.template.html.Execute(&buf, struct {
		Args   interface{}
		Blocks []template.HTML
	}{
		Args:   master.args,
		Blocks: renderedBlocks,
	})

	return buf.String(), nil
}

func (m *Mailset) RenderText(master *Block, blocks ...*Block) (string, error) {
	var buf bytes.Buffer

	// render all the content blocks
	renderedBlocks := make([]string, 0, len(blocks))
	for _, b := range blocks {
		b.template.text.Execute(&buf, struct {
			Args interface{}
		}{
			Args: b.args,
		})

		renderedBlocks = append(renderedBlocks, buf.String())
		buf.Reset()
	}

	// render master, replacing content into it.
	buf.Reset()
	master.template.text.Execute(&buf, struct {
		Args   interface{}
		Blocks []string
	}{
		Args:   master.args,
		Blocks: renderedBlocks,
	})

	return buf.String(), nil
}

func (m *Mailset) GenerateMail(from string, to string, subject string, master *Block, blocks ...*Block) (*Mail, error) {
	html, err := m.RenderHTML(master, blocks...)
	if err != nil {
		return nil, err
	}

	text, err := m.RenderText(master, blocks...)
	if err != nil {
		return nil, err
	}

	return &Mail{
		From:     from,
		To:       []string{to},
		Subject:  subject,
		BodyHTML: html,
		BodyText: text,
	}, nil
}

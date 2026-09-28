package llm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestCatalogEntriesComplete(t *testing.T) {
	entries := Catalog()
	seen := map[string]bool{}
	for _, m := range entries {
		if m.ID == "" || m.Name == "" || m.API == "" || m.Provider == "" || m.BaseURL == "" {
			t.Errorf("%q: missing identity fields: %+v", m.ID, m)
		}
		if seen[m.ID] {
			t.Errorf("%q: duplicate ID", m.ID)
		}
		seen[m.ID] = true
		if m.ContextWindow <= 0 || m.MaxOutput <= 0 || m.MaxOutput > m.ContextWindow {
			t.Errorf("%s: limits context=%d max_output=%d", m.ID, m.ContextWindow, m.MaxOutput)
		}
		if m.Cost.Input <= 0 || m.Cost.Output <= 0 || m.Cost.CacheRead <= 0 {
			t.Errorf("%s: cost %+v has a zero price", m.ID, m.Cost)
		}
		if m.API == AnthropicMessages && m.Cost.CacheWrite <= 0 {
			t.Errorf("%s: anthropic entry has no cache write price", m.ID)
		}
		if m.Quirks.AnthropicAdaptiveThinking && (m.API != AnthropicMessages || !m.Reasoning) {
			t.Errorf("%s: adaptive thinking quirk on a non-reasoning or non-anthropic model", m.ID)
		}
	}
}

// TestCatalogListsEveryEntry keeps Catalog in step with the vars: every
// package-level var in catalog.go is a Model entry, so their count must
// match the list's length.
func TestCatalogListsEveryEntry(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "catalog.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	vars := 0
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.VAR {
			for _, spec := range g.Specs {
				vars += len(spec.(*ast.ValueSpec).Names)
			}
		}
	}
	if got := len(Catalog()); got != vars {
		t.Errorf("catalog.go declares %d Model vars but Catalog() returns %d", vars, got)
	}
}

package langkit_test

import (
	"testing"

	"github.com/oliverkofoed/gokit/langkit"
)

func TestTextExtractor(t *testing.T) {
	extractor := langkit.NewTextExtractor(".")
	extractor.Include("**/*.go")
	extractor.Exclude("**/extractor_test.go")
	extractor.Match(langkit.SimpleGetMatcher("langkit.Get("))
	extractor.Match(langkit.SimpleGetPluralMatcher("langkit.GetPlural("))
	extractor.ExtractToP("testmodule/source.pot", true)
}

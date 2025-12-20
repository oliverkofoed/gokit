package openapikit

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type NestedStruct struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RootArgs struct {
	Items []NestedStruct `json:"items"`
}

type RootResult struct {
	Status string `json:"status"`
}

type FileArgs struct {
	File io.Reader `json:"file"`
}

func TestGenerateOpenAPISchema(t *testing.T) {
	apis := New()
	apis.Add(Method{
		Service: "Test",
		Path:    "/test",
		Name:    "DoTest",
		Action: Action(func(c *web.Context, args RootArgs) (*RootResult, error) {
			return &RootResult{Status: "ok"}, nil
		}),
	})

	schema := apis.GenerateOpenAPISchema()

	// Verify it's OAS 3.1.0
	if schema.OpenAPI != "3.1.0" {
		t.Errorf("expected OpenAPI 3.1.0, got %s", schema.OpenAPI)
	}

	// Check if any schema has "definitions" (it should be flattened)
	b, _ := json.Marshal(schema)
	s := string(b)
	if strings.Contains(s, "\"definitions\":") {
		t.Errorf("Schema contains 'definitions', should be flattened: %s", s)
	}

	// Check if NestedStruct (or some version of it) is in components/schemas
	foundNested := false
	for name := range schema.Components.Schemas {
		if strings.Contains(name, "NestedStruct") {
			foundNested = true
			break
		}
	}
	if !foundNested {
		t.Errorf("NestedStruct schema missing from components/schemas. Schemas: %v", schema.Components.Schemas)
	}

	// Check if any schema has "#/definitions/" (it should be transformed to #/components/schemas/)
	if strings.Contains(s, "#/definitions/") {
		t.Errorf("Schema contains '#/definitions/', should be '#/components/schemas/': %s", s)
	}
}

func TestFileHandling(t *testing.T) {
	apis := New()
	apis.Add(Method{
		Service: "Test",
		Path:    "/upload",
		Name:    "Upload",
		Action: Action(func(c *web.Context, args FileArgs) (*RootResult, error) {
			return &RootResult{Status: "ok"}, nil
		}),
	})

	schema := apis.GenerateOpenAPISchema()
	b, _ := json.Marshal(schema)
	s := string(b)

	if !strings.Contains(s, "multipart/form-data") {
		t.Errorf("Expected multipart/form-data for file upload")
	}

	if !strings.Contains(s, "\"format\":\"binary\"") {
		t.Errorf("Expected binary format for io.Reader field")
	}
}

package openapikit

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type AllTypesArgs struct {
	StringField  string          `json:"string_field"`
	IntField     int             `json:"int_field"`
	Int64Field   int64           `json:"int64_field"`
	BoolField    bool            `json:"bool_field"`
	FloatField   float64         `json:"float_field"`
	SliceField   []string        `json:"slice_field"`
	StructSlice  []NestedStruct  `json:"struct_slice"`
	PointerField *string         `json:"pointer_field"`
	MapField     map[string]int  `json:"map_field"`
	ComplexMap   map[string]Item `json:"complex_map"`
	FileField    io.Reader       `json:"file_field"`
}

type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type AllTypesResult struct {
	Success bool `json:"success"`
}

func TestComprehensiveTypes(t *testing.T) {
	apis := New()
	apis.Add(Method{
		Service: "Comprehensive",
		Path:    "/all-types",
		Name:    "GetAllTypes",
		Action: Action(func(c *web.Context, args AllTypesArgs) (*AllTypesResult, error) {
			return &AllTypesResult{Success: true}, nil
		}),
	})

	schema := apis.GenerateOpenAPISchema()
	b, _ := json.MarshalIndent(schema, "", "  ")
	s := string(b)

	// Basic type checks
	expectedSubstrings := []string{
		"\"string_field\"", "\"type\": \"string\"",
		"\"int_field\"", "\"type\": \"integer\"",
		"\"bool_field\"", "\"type\": \"boolean\"",
		"\"float_field\"", "\"type\": \"number\"",
		"\"slice_field\"", "\"array\"",
		"\"struct_slice\"",
		"\"map_field\"", "\"additionalProperties\"",
		"\"multipart/form-data\"",
		"\"format\": \"binary\"",
	}

	for _, substr := range expectedSubstrings {
		if !contains(s, substr) {
			t.Errorf("Expected schema to contain %s, but it didn't.", substr)
		}
	}
	if t.Failed() {
		t.Logf("Full Schema:\n%s", s)
	}
}

func TestSharedTypes(t *testing.T) {
	apis := New()

	type SharedStruct struct {
		Value string `json:"value"`
	}

	type Args1 struct {
		Shared SharedStruct `json:"shared"`
	}

	type Args2 struct {
		Items []SharedStruct `json:"items"`
	}

	apis.Add(Method{
		Service: "Service1",
		Path:    "/op1",
		Name:    "Op1",
		Action: Action(func(c *web.Context, args Args1) (*AllTypesResult, error) {
			return &AllTypesResult{Success: true}, nil
		}),
	})

	apis.Add(Method{
		Service: "Service2",
		Path:    "/op2",
		Name:    "Op2",
		Action: Action(func(c *web.Context, args Args2) (*AllTypesResult, error) {
			return &AllTypesResult{Success: true}, nil
		}),
	})

	schema := apis.GenerateOpenAPISchema()

	// SharedStruct should only appear ONCE in components/schemas
	// Note: names are sanitized and prefixed with package name for anonymous/local types in tests
	count := 0
	for name := range schema.Components.Schemas {
		if strings.Contains(name, "SharedStruct") {
			count++
		}
	}

	if count != 1 {
		t.Errorf("Expected SharedStruct to be registered exactly once, got %d. Schemas: %v", count, schema.Components.Schemas)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// Helper to make the code compile if I missed an import or used a wrong helper
func init() {
	_ = io.EOF
}

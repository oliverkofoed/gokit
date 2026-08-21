package agentkit

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// kitchenSink exercises every supported field shape (R28).
type kitchenSink struct {
	Name     string            `json:"name" jsonschema:"description=The name, relative to the workspace"`
	Mode     string            `json:"mode,omitempty" jsonschema:"enum=create|overwrite,default=create"`
	Count    int               `json:"count" jsonschema:"minimum=1,maximum=10"`
	Ratio    float64           `json:"ratio,omitempty"`
	Deep     bool              `json:"deep"`
	Tags     []string          `json:"tags,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Nested   sinkNested        `json:"nested"`
	Optional *int              `json:"optional"`
	Skipped  string            `json:"-"`
	hidden   string            //nolint:unused // exercises unexported skipping
}

type sinkNested struct {
	Path string `json:"path"`
}

func TestSchemaGeneration(t *testing.T) {
	node := buildSchema(reflect.TypeFor[kitchenSink]())
	got := string(mustMarshalSchema(node))

	// Structural spot checks beat a brittle full-string golden here; the
	// validator tests pin down semantics.
	for _, want := range []string{
		`"type":"object"`,
		`"additionalProperties":false`,
		`"description":"The name, relative to the workspace"`, // comma survived (R28)
		`"enum":["create","overwrite"]`,
		`"default":"create"`,
		`"minimum":1`,
		`"maximum":10`,
		`"items":{"type":"string"}`,
		`"path":{"type":"string"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema missing %s\nschema: %s", want, got)
		}
	}

	var parsed struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	// Required: non-pointer, non-omitempty fields only.
	wantRequired := []string{"name", "count", "deep", "nested"}
	if !reflect.DeepEqual(parsed.Required, wantRequired) {
		t.Errorf("required = %v, want %v", parsed.Required, wantRequired)
	}
	if _, ok := parsed.Properties["Skipped"]; ok {
		t.Error(`json:"-" field present in properties`)
	}
	if _, ok := parsed.Properties["-"]; ok {
		t.Error(`json:"-" field present as "-"`)
	}
	if _, ok := parsed.Properties["hidden"]; ok {
		t.Error("unexported field present in properties")
	}
	// map → object with additionalProperties schema
	if !strings.Contains(string(parsed.Properties["env"]), `"additionalProperties":{"type":"string"}`) {
		t.Errorf("env schema = %s", parsed.Properties["env"])
	}
}

func TestSchemaUnsupported(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn()
		})
	}

	type hasChan struct {
		C chan int `json:"c"`
	}
	mustPanic("chan field", func() {
		NewTool("x", "d", func(ctx context.Context, a hasChan) (ToolResult, error) { return ToolResult{}, nil })
	})

	type hasFunc struct {
		F func() `json:"f"`
	}
	mustPanic("func field", func() {
		NewTool("x", "d", func(ctx context.Context, a hasFunc) (ToolResult, error) { return ToolResult{}, nil })
	})

	type hasIface struct {
		V any `json:"v"`
	}
	mustPanic("interface field", func() {
		NewTool("x", "d", func(ctx context.Context, a hasIface) (ToolResult, error) { return ToolResult{}, nil })
	})

	type intKeys struct {
		M map[int]string `json:"m"`
	}
	mustPanic("non-string map keys", func() {
		NewTool("x", "d", func(ctx context.Context, a intKeys) (ToolResult, error) { return ToolResult{}, nil })
	})

	type badDirective struct {
		S string `json:"s" jsonschema:"pattern=^x$"`
	}
	mustPanic("unknown directive", func() {
		NewTool("x", "d", func(ctx context.Context, a badDirective) (ToolResult, error) { return ToolResult{}, nil })
	})

	type enumOnInt struct {
		N int `json:"n" jsonschema:"enum=1|2"`
	}
	mustPanic("enum on non-string", func() {
		NewTool("x", "d", func(ctx context.Context, a enumOnInt) (ToolResult, error) { return ToolResult{}, nil })
	})

	type deep struct {
		Next *deep `json:"next"`
	}
	mustPanic("recursion depth", func() {
		NewTool("x", "d", func(ctx context.Context, a deep) (ToolResult, error) { return ToolResult{}, nil })
	})

	mustPanic("non-struct args", func() {
		NewTool("x", "d", func(ctx context.Context, a string) (ToolResult, error) { return ToolResult{}, nil })
	})
}

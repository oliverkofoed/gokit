package agentkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type validArgs struct {
	Path  string   `json:"path"`
	Mode  string   `json:"mode,omitempty" jsonschema:"enum=r|w"`
	Count int      `json:"count,omitempty" jsonschema:"minimum=1,maximum=3"`
	Tags  []string `json:"tags,omitempty"`
}

// TestValidation drives the R29 validator through a NewTool's Execute.
func TestValidation(t *testing.T) {
	var called bool
	var got validArgs
	tool := NewTool("t", "d", func(ctx context.Context, a validArgs) (ToolResult, error) {
		called = true
		got = a
		return Text("ok"), nil
	})

	cases := map[string]struct {
		args    string
		wantErr string // "" = valid
	}{
		"valid":               {`{"path":"a.go","mode":"r","count":2,"tags":["x"]}`, ""},
		"missing required":    {`{}`, "args.path: required property missing"},
		"wrong type":          {`{"path":42}`, "args.path: expected string, got number"},
		"unknown property":    {`{"path":"a","bogus":1}`, "args.bogus: unknown property"},
		"enum violation":      {`{"path":"a","mode":"x"}`, `args.mode: "x" is not one of [r, w]`},
		"below minimum":       {`{"path":"a","count":0}`, "args.count: 0 is below minimum 1"},
		"above maximum":       {`{"path":"a","count":9}`, "args.count: 9 is above maximum 3"},
		"non-integral":        {`{"path":"a","count":1.5}`, "non-integral"},
		"array element":       {`{"path":"a","tags":[1]}`, "args.tags[0]: expected string, got number"},
		"not JSON":            {`{`, "not valid JSON"},
		"empty args = object": {``, "args.path: required property missing"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			called = false
			_, err := tool.Execute(context.Background(), ToolCall{ID: "1", Name: "t", Args: json.RawMessage(tc.args)})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !called || got.Path != "a.go" || got.Count != 2 {
					t.Fatalf("fn not called with decoded args: called=%v got=%+v", called, got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if called {
				t.Fatal("Execute fn called with invalid args (R29)")
			}
		})
	}
}

func TestTextHelper(t *testing.T) {
	r := Text("wrote %d bytes to %s", 5, "x.go")
	if len(r.Blocks) != 1 || r.Blocks[0].Text != "wrote 5 bytes to x.go" {
		t.Fatalf("result = %+v", r)
	}
}

func TestProgressNoopOutsideSession(t *testing.T) {
	// Must not panic and must not do anything observable.
	Progress(context.Background(), Text("ignored"))
}

func TestSchemaValidateOnSessionLevel(t *testing.T) {
	// Session-level validation (t.validate) fires before BeforeTool: a
	// blocked-by-policy tool with invalid args reports the validation error,
	// not the policy error (§11.3 order).
	tool := NewTool("t", "d", func(ctx context.Context, a validArgs) (ToolResult, error) {
		t.Fatal("Execute must not run")
		return ToolResult{}, nil
	})
	if tool.validate == nil {
		t.Fatal("NewTool must set validate")
	}
	err := tool.validate(json.RawMessage(`{"path":1}`))
	if err == nil || !strings.Contains(err.Error(), "args.path") {
		t.Fatalf("validate err = %v", err)
	}
}

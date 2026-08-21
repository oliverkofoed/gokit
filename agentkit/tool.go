package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

// ToolCall is one tool invocation requested by the model.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult is what a tool returns.
type ToolResult struct {
	// Blocks (text and/or images) are sent back to the model.
	Blocks []llm.Block
	// Details is app-facing data (UIs, logs); never sent to the model. Must
	// be JSON-serializable if session state will be persisted.
	Details any
}

// Text builds a single-text-block result via fmt.Sprintf.
func Text(format string, a ...any) ToolResult {
	return ToolResult{Blocks: []llm.Block{llm.TextBlock(fmt.Sprintf(format, a...))}}
}

// Tool is a capability the model can invoke.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema for arguments
	// Sequential forces the whole batch containing this tool to run
	// one-at-a-time (R33).
	Sequential bool
	// Execute runs the tool. Returning an error produces an is_error tool
	// result carrying err.Error() — the model sees it and can retry.
	Execute func(ctx context.Context, call ToolCall) (ToolResult, error)

	// validate, when set (NewTool), checks raw arguments against the schema
	// before BeforeTool and Execute run (R29 ordering in §11.3).
	validate func(args json.RawMessage) error
}

// NewTool derives Schema from Args by reflection (R28) and decodes+validates
// arguments before fn runs (R29). Args must be a struct. Construction panics
// on unsupported Args shapes or malformed jsonschema tags — fail fast, at
// startup.
func NewTool[Args any](name, description string, fn func(ctx context.Context, args Args) (ToolResult, error)) Tool {
	t := reflect.TypeFor[Args]()
	node := buildSchema(t)
	validate := func(raw json.RawMessage) error {
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("arguments are not valid JSON: %w", err)
		}
		if errs := validateSchema(node, decoded, "args"); len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}
	return Tool{
		Name:        name,
		Description: description,
		Schema:      mustMarshalSchema(node),
		validate:    validate,
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			raw := call.Args
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			if err := validate(raw); err != nil {
				return ToolResult{}, err
			}
			var args Args
			if err := json.Unmarshal(raw, &args); err != nil {
				return ToolResult{}, fmt.Errorf("decoding arguments: %w", err)
			}
			return fn(ctx, args)
		},
	}
}

type progressKey struct{}

type progressFunc func(ToolResult)

// Progress emits a tool_update event from inside a running tool. It is a
// no-op if ctx does not originate from a session run.
func Progress(ctx context.Context, update ToolResult) {
	if fn, ok := ctx.Value(progressKey{}).(progressFunc); ok {
		fn(update)
	}
}

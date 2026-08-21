package llm

import "encoding/json"

// ToolDef describes a tool to the model. Schema is standard JSON Schema
// (draft 2020-12 subset; protocols sanitize per their endpoint's rules).
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Effort is the uniform reasoning level, mapped per protocol (R14). On models
// with Reasoning: false it is silently ignored.
type Effort string

const (
	EffortOff    Effort = "" // zero value: no reasoning requested
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

// Request is one completion request. It is a value: the client never mutates
// the caller's Messages slice or Model (R5).
type Request struct {
	Model    Model
	System   string
	Messages []Message
	Tools    []ToolDef

	Reasoning   Effort
	MaxTokens   int      // 0 → Model.MaxOutput
	Temperature *float64 // nil → provider default

	// DisableCache turns off automatic prompt-cache breakpoints (R15).
	// Zero value = caching enabled.
	DisableCache bool

	// APIKey overrides client-level auth for this request (R11 step 1).
	APIKey string
}

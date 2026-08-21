package llm

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// Role identifies who a message is from.
type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "tool_result"
)

// StopReason records how an assistant message's generation ended.
type StopReason string

const (
	StopEnd     StopReason = "stop"     // normal completion
	StopLength  StopReason = "length"   // output token limit
	StopToolUse StopReason = "tool_use" // model wants tool results
	StopError   StopReason = "error"    // request failed mid-flight
	StopAborted StopReason = "aborted"  // context cancelled / interrupted
)

// BlockType discriminates Block.
type BlockType string

const (
	BlockText     BlockType = "text"
	BlockThinking BlockType = "thinking"
	BlockImage    BlockType = "image"
	BlockToolCall BlockType = "tool_call"
)

// Block is a tagged union (P6). Only the fields for its Type are set.
type Block struct {
	Type BlockType `json:"type"`

	// BlockText, BlockThinking
	Text string `json:"text,omitempty"`
	// Signature carries opaque provider replay metadata: Anthropic thinking
	// signatures, OpenAI Responses reasoning ids/encrypted content, Gemini
	// thought signatures. Preserve verbatim; never inspect.
	Signature string `json:"signature,omitempty"`
	// Redacted marks safety-redacted thinking (content lives in Signature).
	Redacted bool `json:"redacted,omitempty"`

	// BlockImage
	Data     string `json:"data,omitempty"`      // base64, no data: prefix
	MimeType string `json:"mime_type,omitempty"` // "image/png", "image/jpeg", ...

	// BlockToolCall
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"` // decoded JSON object; "{}" while empty
}

// Usage counts tokens for one assistant message. Input, CacheRead and
// CacheWrite are disjoint counts over the same prompt where the provider
// reports them that way (Anthropic); otherwise CacheRead is the cached subset
// as reported.
type Usage struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	TotalCost  float64 `json:"total_cost"` // USD, computed from Model.Cost (R4)
}

// Message is one conversation entry (P6: tagged struct, lossless JSON).
type Message struct {
	Role   Role      `json:"role"`
	Blocks []Block   `json:"blocks"`
	Time   time.Time `json:"time"`

	// Kind marks an app-level message (UI notification, divider, compaction
	// marker, audit note). Empty = a normal LLM message. Non-empty-Kind
	// messages live in history and State JSON but are never sent to the
	// model (R37). Role and Blocks may be empty on Kind messages.
	Kind string `json:"kind,omitempty"`
	// Meta carries app data for Kind messages; opaque to the library.
	Meta json.RawMessage `json:"meta,omitempty"`

	// RoleToolResult only:
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`

	// RoleAssistant only (response metadata):
	Model      string     `json:"model,omitempty"`    // Model.ID that produced it
	Provider   string     `json:"provider,omitempty"` // Model.Provider
	API        API        `json:"api,omitempty"`      // Model.API
	StopReason StopReason `json:"stop_reason,omitempty"`
	ErrorText  string     `json:"error_text,omitempty"` // set when StopReason is error/aborted
	Usage      *Usage     `json:"usage,omitempty"`
}

// UserText builds a user message with one text block (R3).
func UserText(text string) Message {
	return Message{Role: RoleUser, Blocks: []Block{TextBlock(text)}, Time: time.Now()}
}

// UserBlocks builds a user message from content blocks (R3).
func UserBlocks(blocks ...Block) Message {
	return Message{Role: RoleUser, Blocks: blocks, Time: time.Now()}
}

// TextBlock builds a text content block.
func TextBlock(text string) Block {
	return Block{Type: BlockText, Text: text}
}

// ImageBlock builds an image content block, base64-encoding data.
func ImageBlock(mimeType string, data []byte) Block {
	return Block{Type: BlockImage, MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}
}

// ToolResultMessage builds a tool result message (R3).
func ToolResultMessage(callID, toolName string, isError bool, blocks ...Block) Message {
	return Message{
		Role:       RoleToolResult,
		ToolCallID: callID,
		ToolName:   toolName,
		IsError:    isError,
		Blocks:     blocks,
		Time:       time.Now(),
	}
}

// AppMessage builds an app-level Kind message (R37): preserved in history and
// State, never sent to the model.
func AppMessage(kind string, meta json.RawMessage) Message {
	return Message{Kind: kind, Meta: meta, Time: time.Now()}
}

// ComputeCost computes USD cost for usage against a model's price table (R4).
func ComputeCost(m Model, u Usage) float64 {
	return (float64(u.Input)*m.Cost.Input +
		float64(u.Output)*m.Cost.Output +
		float64(u.CacheRead)*m.Cost.CacheRead +
		float64(u.CacheWrite)*m.Cost.CacheWrite) / 1e6
}

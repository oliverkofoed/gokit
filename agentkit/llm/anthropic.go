package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/oliverkofoed/gokit/agentkit/llm/internal/sse"
)

// streamAnthropic implements the anthropic-messages protocol (SPEC §8.1).
// EventStart is emitted by the caller; history normalization and MaxTokens
// defaulting are already applied. It emits raw content events per the
// NewStream producer contract plus exactly one DoneEvent or ErrorEvent.
func (c *Client) streamAnthropic(ctx context.Context, req Request, apiKey string, emit func(Event) bool) {
	payload := anthBuildPayload(c, req)

	header := http.Header{}
	if apiKey != NoAuth {
		header.Set("x-api-key", apiKey)
	}
	header.Set("anthropic-version", "2023-06-01")
	for k, v := range req.Model.Headers {
		header.Set(k, v)
	}

	url := strings.TrimSuffix(req.Model.BaseURL, "/") + "/v1/messages"
	resp, err := c.doJSON(ctx, http.MethodPost, url, header, payload)
	if err != nil {
		emit(ErrorEvent(err, Usage{}))
		return
	}
	defer resp.Body.Close()

	anthDecodeSSE(resp.Body, emit)
}

// ---- request encoding -------------------------------------------------------

// anthCacheControl marks a prompt-cache breakpoint (R15).
type anthCacheControl struct {
	Type string `json:"type"`
}

type anthThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	InputSchema  json.RawMessage   `json:"input_schema"`
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

type anthImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthBlock is one content block in any position (system, message content,
// tool_result nested content). Only the fields for its Type are set.
type anthBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`
	// thinking / redacted_thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
	// image
	Source *anthImageSource `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string      `json:"tool_use_id,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
	Content   []anthBlock `json:"content,omitempty"`

	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

type anthMessage struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

type anthRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
	System      []anthBlock   `json:"system,omitempty"`
	Messages    []anthMessage `json:"messages"`
	Tools       []anthTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Thinking    *anthThinking `json:"thinking,omitempty"`
}

// anthBuildPayload maps a normalized Request onto the wire body (SPEC §8.1).
func anthBuildPayload(c *Client, req Request) *anthRequest {
	p := &anthRequest{
		Model:       req.Model.ID,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		Temperature: req.Temperature,
	}
	cache := func() *anthCacheControl {
		if req.DisableCache {
			return nil
		}
		return &anthCacheControl{Type: "ephemeral"}
	}

	if req.System != "" {
		p.System = []anthBlock{{Type: "text", Text: req.System, CacheControl: cache()}}
	}

	// R14: thinking enabled → budget from effort, temperature omitted, and
	// max_tokens must exceed the budget.
	if req.Reasoning != "" && req.Model.Reasoning {
		budget := c.thinkingBudget(req.Reasoning)
		p.Thinking = &anthThinking{Type: "enabled", BudgetTokens: budget}
		p.Temperature = nil
		if p.MaxTokens <= budget {
			p.MaxTokens = budget + 4096
		}
	}

	for _, t := range req.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		p.Tools = append(p.Tools, anthTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if len(p.Tools) > 0 {
		p.Tools[len(p.Tools)-1].CacheControl = cache()
	}

	p.Messages = anthEncodeMessages(req.Messages)

	// R15: breakpoint on the last content block of the final message. Thinking
	// blocks cannot carry cache_control (API constraint), so skip those.
	if !req.DisableCache && len(p.Messages) > 0 {
		last := &p.Messages[len(p.Messages)-1]
		if n := len(last.Content); n > 0 {
			b := &last.Content[n-1]
			if b.Type != "thinking" && b.Type != "redacted_thinking" {
				b.CacheControl = &anthCacheControl{Type: "ephemeral"}
			}
		}
	}
	return p
}

// anthEncodeMessages maps normalized history to wire messages. Consecutive
// tool results merge into one user-role message (SPEC §8.1).
func anthEncodeMessages(msgs []Message) []anthMessage {
	var out []anthMessage
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case RoleUser:
			out = append(out, anthMessage{Role: "user", Content: anthUserBlocks(m.Blocks)})
		case RoleToolResult:
			content := []anthBlock{anthToolResultBlock(m)}
			for i+1 < len(msgs) && msgs[i+1].Role == RoleToolResult {
				i++
				content = append(content, anthToolResultBlock(msgs[i]))
			}
			out = append(out, anthMessage{Role: "user", Content: content})
		case RoleAssistant:
			out = append(out, anthMessage{Role: "assistant", Content: anthAssistantBlocks(m.Blocks)})
		}
	}
	return out
}

func anthUserBlocks(blocks []Block) []anthBlock {
	var out []anthBlock
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			out = append(out, anthBlock{Type: "text", Text: b.Text})
		case BlockImage:
			out = append(out, anthBlock{Type: "image", Source: &anthImageSource{
				Type: "base64", MediaType: b.MimeType, Data: b.Data,
			}})
		}
	}
	return out
}

func anthToolResultBlock(m Message) anthBlock {
	return anthBlock{
		Type:      "tool_result",
		ToolUseID: m.ToolCallID,
		IsError:   m.IsError,
		Content:   anthUserBlocks(m.Blocks),
	}
}

// anthAssistantBlocks encodes assistant content. Any thinking block still
// present is same-model (R17 normalization already textified cross-model
// ones), so it replays natively with its signature.
func anthAssistantBlocks(blocks []Block) []anthBlock {
	var out []anthBlock
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			out = append(out, anthBlock{Type: "text", Text: b.Text})
		case BlockThinking:
			if b.Redacted {
				out = append(out, anthBlock{Type: "redacted_thinking", Data: b.Signature})
			} else {
				out = append(out, anthBlock{Type: "thinking", Thinking: b.Text, Signature: b.Signature})
			}
		case BlockToolCall:
			args := b.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, anthBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: args})
		}
	}
	return out
}

// ---- SSE decoding -----------------------------------------------------------

type anthUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// anthSSEEvent is the union of all anthropic-messages stream event payloads.
type anthSSEEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage anthUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthDecodeSSE turns the response event stream into raw llm.Events. It emits
// exactly one terminal event (SPEC §8.1); any read or parse failure becomes an
// ErrorEvent carrying the usage seen so far.
func anthDecodeSSE(body io.Reader, emit func(Event) bool) {
	rd := sse.NewReader(body)
	var (
		usage      Usage
		stop       StopReason
		refused    bool
		blockTypes = map[int]string{}
		redacted   = map[int]string{}
	)
	fail := func(err error) { emit(ErrorEvent(err, usage)) }

	for {
		sev, err := rd.Next()
		if err != nil {
			if err == io.EOF {
				fail(errors.New("llm: anthropic: stream ended before message_stop"))
			} else {
				fail(fmt.Errorf("llm: anthropic: stream read: %w", err))
			}
			return
		}
		if strings.TrimSpace(sev.Data) == "" {
			continue
		}
		var ev anthSSEEvent
		if err := json.Unmarshal([]byte(sev.Data), &ev); err != nil {
			fail(fmt.Errorf("llm: anthropic: malformed SSE payload %q: %w", sev.Data, err))
			return
		}
		typ := ev.Type
		if typ == "" {
			typ = sev.Name
		}

		switch typ {
		case "message_start":
			if ev.Message != nil {
				usage.Input = ev.Message.Usage.InputTokens
				usage.Output = ev.Message.Usage.OutputTokens
				usage.CacheWrite = ev.Message.Usage.CacheCreationInputTokens
				usage.CacheRead = ev.Message.Usage.CacheReadInputTokens
			}

		case "content_block_start":
			if ev.ContentBlock == nil {
				continue
			}
			blockTypes[ev.Index] = ev.ContentBlock.Type
			switch ev.ContentBlock.Type {
			case "text":
				if !emit(Event{Type: EventTextStart, Index: ev.Index}) {
					return
				}
			case "thinking":
				if !emit(Event{Type: EventThinkingStart, Index: ev.Index}) {
					return
				}
			case "redacted_thinking":
				redacted[ev.Index] = ev.ContentBlock.Data
				if !emit(Event{Type: EventThinkingStart, Index: ev.Index}) {
					return
				}
			case "tool_use":
				b := &Block{Type: BlockToolCall, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
				if !emit(Event{Type: EventToolCallStart, Index: ev.Index, Block: b}) {
					return
				}
			}

		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if !emit(Event{Type: EventTextDelta, Index: ev.Index, Delta: ev.Delta.Text}) {
					return
				}
			case "thinking_delta":
				if !emit(Event{Type: EventThinkingDelta, Index: ev.Index, Delta: ev.Delta.Thinking}) {
					return
				}
			case "signature_delta":
				if !emit(Event{Type: EventThinkingDelta, Index: ev.Index, signatureDelta: ev.Delta.Signature}) {
					return
				}
			case "input_json_delta":
				if !emit(Event{Type: EventToolCallDelta, Index: ev.Index, Delta: ev.Delta.PartialJSON}) {
					return
				}
			}

		case "content_block_stop":
			switch blockTypes[ev.Index] {
			case "text":
				if !emit(Event{Type: EventTextEnd, Index: ev.Index}) {
					return
				}
			case "thinking":
				if !emit(Event{Type: EventThinkingEnd, Index: ev.Index}) {
					return
				}
			case "redacted_thinking":
				b := &Block{Type: BlockThinking, Redacted: true, Signature: redacted[ev.Index]}
				if !emit(Event{Type: EventThinkingEnd, Index: ev.Index, Block: b}) {
					return
				}
			case "tool_use":
				if !emit(Event{Type: EventToolCallEnd, Index: ev.Index}) {
					return
				}
			}

		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				if ev.Delta.StopReason == "refusal" {
					refused = true
				}
				stop = anthMapStop(ev.Delta.StopReason)
			}
			if ev.Usage != nil {
				usage.Output = ev.Usage.OutputTokens
			}

		case "message_stop":
			if refused {
				fail(errors.New("refusal"))
				return
			}
			emit(DoneEvent(stop, usage))
			return

		case "error":
			msg, etype := "unknown error", "unknown"
			if ev.Error != nil {
				msg, etype = ev.Error.Message, ev.Error.Type
			}
			fail(fmt.Errorf("llm: anthropic: %s: %s", etype, msg))
			return

		case "ping":
			// keepalive

		default:
			// unknown event types: ignored for forward compatibility
		}
	}
}

// anthMapStop maps anthropic stop reasons onto StopReason (SPEC §8.1).
// "refusal" is handled separately (it terminates as an error).
func anthMapStop(s string) StopReason {
	switch s {
	case "end_turn":
		return StopEnd
	case "max_tokens":
		return StopLength
	case "tool_use":
		return StopToolUse
	default:
		return StopEnd
	}
}

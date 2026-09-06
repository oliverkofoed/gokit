package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/oliverkofoed/gokit/agentkit/llm/internal/sse"
)

// streamOpenAIChat implements the openai-chat protocol (SPEC §8.2). The
// caller has already emitted EventStart, normalized history (§5.3), and
// defaulted MaxTokens; this emits content events per the NewStream producer
// contract plus exactly one terminal event.
func (c *Client) streamOpenAIChat(ctx context.Context, req Request, apiKey string, emit func(Event) bool) {
	header := http.Header{}
	if apiKey != NoAuth {
		header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range req.Model.Headers {
		header.Set(k, v)
	}
	url := strings.TrimSuffix(req.Model.BaseURL, "/") + "/chat/completions"

	resp, err := c.doJSON(ctx, http.MethodPost, url, header, oaicPayload(req))
	if err != nil {
		emit(ErrorEvent(err, Usage{}))
		return
	}
	defer resp.Body.Close()

	d := &oaicDecoder{emit: emit, textIdx: -1, tools: map[int]*oaicToolCall{}}
	r := sse.NewReader(resp.Body)
	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			emit(ErrorEvent(fmt.Errorf("llm: openai-chat: read stream: %w", err), d.usage))
			return
		}
		data := strings.TrimSpace(ev.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		if !d.chunk(data) {
			return // terminal event emitted, or the consumer stopped
		}
	}
	d.finish()
}

// ---- request encoding (SPEC §8.2) ------------------------------------------

// oaicPayload builds the Chat Completions request body.
func oaicPayload(req Request) map[string]any {
	q := req.Model.Quirks
	body := map[string]any{
		"model":    req.Model.ID,
		"messages": oaicMessages(req),
		"stream":   true,
	}
	if !q.NoStreamUsage {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if req.MaxTokens > 0 {
		field := "max_completion_tokens"
		if q.MaxTokensField != "" {
			field = q.MaxTokensField
		}
		body[field] = req.MaxTokens
	}
	// R14. EffortOff means "no reasoning requested", which is not the same as
	// saying nothing: on a model that reasons by default, omitting the field
	// leaves reasoning on. ReasoningEffortNone makes the intent explicit.
	switch {
	case q.NoReasoningEffort || !req.Model.Reasoning:
		// omitted entirely
	case req.Reasoning != "":
		body["reasoning_effort"] = string(req.Reasoning)
	case q.ReasoningEffortNone:
		body["reasoning_effort"] = "none"
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Schema
			if len(schema) == 0 {
				schema = json.RawMessage("{}")
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  schema,
				},
			})
		}
		body["tools"] = tools
	}
	return body
}

// oaicMessages encodes the system prompt and conversation history as wire
// messages, applying OpenRouter cache_control breakpoints when the quirk is
// set (R15).
func oaicMessages(req Request) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": oaicUserContent(m.Blocks)})
		case RoleAssistant:
			msgs = append(msgs, oaicAssistantMessage(m))
		case RoleToolResult:
			tool, spill := oaicToolResult(m)
			msgs = append(msgs, tool)
			if spill != nil {
				msgs = append(msgs, spill)
			}
		}
	}
	if req.Model.Quirks.AnthropicCacheControl && !req.DisableCache {
		oaicApplyCacheControl(msgs)
	}
	return msgs
}

// oaicUserContent encodes user blocks: a plain string when the message is a
// single text block, otherwise an array of text/image_url/file parts.
func oaicUserContent(blocks []Block) any {
	if len(blocks) == 1 && blocks[0].Type == BlockText {
		return blocks[0].Text
	}
	parts := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, oaicTextPart(b.Text))
		case BlockImage:
			parts = append(parts, oaicImagePart(b))
		case BlockDocument:
			parts = append(parts, oaicFilePart(b))
		}
	}
	return parts
}

func oaicTextPart(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func oaicImagePart(b Block) map[string]any {
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "data:" + b.MimeType + ";base64," + b.Data},
	}
}

// oaicFilePart encodes a document as an inline file part (SPEC §8.2). The
// name is sent as the filename: this protocol has nowhere else to put it,
// and an endpoint that infers a type from the extension needs it.
func oaicFilePart(b Block) map[string]any {
	file := map[string]any{"file_data": "data:" + b.MimeType + ";base64," + b.Data}
	if b.Name != "" {
		file["filename"] = b.Name
	}
	return map[string]any{"type": "file", "file": file}
}

// oaicAssistantMessage encodes an assistant message: text content (or null)
// plus tool_calls with arguments re-serialized as a JSON string. Thinking
// blocks are dropped — normalization (R17) already textified cross-model
// ones, and same-model thinking is not replayable on this protocol.
func oaicAssistantMessage(m Message) map[string]any {
	var texts []string
	var calls []any
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			texts = append(texts, b.Text)
		case BlockToolCall:
			args := "{}"
			if len(b.Args) > 0 {
				args = string(b.Args)
			}
			calls = append(calls, map[string]any{
				"id":       b.ID,
				"type":     "function",
				"function": map[string]any{"name": b.Name, "arguments": args},
			})
		}
	}
	out := map[string]any{"role": "assistant"}
	if len(texts) > 0 {
		out["content"] = strings.Join(texts, "\n")
	} else {
		out["content"] = nil
	}
	if len(calls) > 0 {
		out["tool_calls"] = calls
	}
	return out
}

// oaicToolResult encodes a tool result as a role:tool message. Image blocks
// spill into an immediately following user message, since role:tool cannot
// carry images.
func oaicToolResult(m Message) (tool, spill map[string]any) {
	var texts []string
	var images []any
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			texts = append(texts, b.Text)
		case BlockImage:
			images = append(images, oaicImagePart(b))
		}
	}
	tool = map[string]any{
		"role":         "tool",
		"tool_call_id": m.ToolCallID,
		"content":      strings.Join(texts, "\n"),
	}
	if len(images) > 0 {
		spill = map[string]any{"role": "user", "content": images}
	}
	return tool, spill
}

// oaicApplyCacheControl adds OpenRouter-style Anthropic cache_control
// breakpoints (R15): on the system message and on the last text part of the
// final message.
func oaicApplyCacheControl(msgs []map[string]any) {
	if len(msgs) == 0 {
		return
	}
	if msgs[0]["role"] == "system" {
		oaicTagLastText(msgs[0])
	}
	last := msgs[len(msgs)-1]
	if len(msgs) > 1 || msgs[0]["role"] != "system" {
		oaicTagLastText(last)
	}
}

// oaicTagLastText converts string content to a one-part array and marks the
// last text part with cache_control. Messages without a text part (null
// content, image-only spillover) are left untouched.
func oaicTagLastText(msg map[string]any) {
	cc := map[string]any{"type": "ephemeral"}
	switch content := msg["content"].(type) {
	case string:
		part := oaicTextPart(content)
		part["cache_control"] = cc
		msg["content"] = []any{part}
	case []any:
		for i := len(content) - 1; i >= 0; i-- {
			if part, ok := content[i].(map[string]any); ok && part["type"] == "text" {
				part["cache_control"] = cc
				return
			}
		}
	}
}

// ---- SSE decoding (SPEC §8.2) -----------------------------------------------

// oaicChunk is one `data:` payload of the Chat Completions stream.
type oaicChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	// Some compatible endpoints (OpenRouter) deliver mid-stream failures as
	// an error object in a data chunk rather than an HTTP status.
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// oaicToolCall tracks one streaming tool call, keyed by the wire
// tool_calls[].index.
type oaicToolCall struct {
	blockIdx int
	id       string
	name     string
}

// oaicDecoder folds Chat Completions SSE chunks into raw stream events.
type oaicDecoder struct {
	emit  func(Event) bool
	usage Usage

	textIdx int // block index of the lazily opened text block; -1 when none
	nextIdx int // next free block index
	tools   map[int]*oaicToolCall
	closed  bool // open blocks already closed (finish_reason seen)

	stop     StopReason
	filtered bool // finish_reason == "content_filter"
}

// chunk processes one data payload. It returns false when the producer must
// stop: a terminal event was emitted, or the consumer stopped.
func (d *oaicDecoder) chunk(data string) bool {
	var ch oaicChunk
	if err := json.Unmarshal([]byte(data), &ch); err != nil {
		d.emit(ErrorEvent(fmt.Errorf("llm: openai-chat: malformed chunk: %w", err), d.usage))
		return false
	}
	if ch.Error != nil {
		d.emit(ErrorEvent(fmt.Errorf("llm: openai-chat: provider error: %s", ch.Error.Message), d.usage))
		return false
	}
	if ch.Usage != nil {
		// May arrive in a chunk with empty choices (stream_options usage).
		d.usage.Input = ch.Usage.PromptTokens
		d.usage.Output = ch.Usage.CompletionTokens
		d.usage.CacheRead = ch.Usage.PromptTokensDetails.CachedTokens
	}
	for _, choice := range ch.Choices {
		if choice.Delta.Content != "" {
			if d.textIdx < 0 { // open the text block lazily at the next free index
				d.textIdx = d.nextIdx
				d.nextIdx++
				if !d.emit(Event{Type: EventTextStart, Index: d.textIdx}) {
					return false
				}
			}
			if !d.emit(Event{Type: EventTextDelta, Index: d.textIdx, Delta: choice.Delta.Content}) {
				return false
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Index < 0 {
				d.emit(ErrorEvent(fmt.Errorf("llm: openai-chat: negative tool_calls index %d", tc.Index), d.usage))
				return false
			}
			st := d.tools[tc.Index]
			if st == nil {
				offset := 0
				if d.textIdx >= 0 {
					offset = 1
				}
				st = &oaicToolCall{blockIdx: offset + tc.Index, id: tc.ID, name: tc.Function.Name}
				d.tools[tc.Index] = st
				if st.blockIdx >= d.nextIdx {
					d.nextIdx = st.blockIdx + 1
				}
				if !d.emit(Event{
					Type:  EventToolCallStart,
					Index: st.blockIdx,
					Block: &Block{Type: BlockToolCall, ID: st.id, Name: st.name},
				}) {
					return false
				}
			} else {
				if tc.ID != "" {
					st.id = tc.ID
				}
				if tc.Function.Name != "" {
					st.name = tc.Function.Name
				}
			}
			if tc.Function.Arguments != "" {
				if !d.emit(Event{Type: EventToolCallDelta, Index: st.blockIdx, Delta: tc.Function.Arguments}) {
					return false
				}
			}
		}
		if choice.FinishReason != "" {
			if !d.finishReason(choice.FinishReason) {
				return false
			}
		}
	}
	return true
}

// finishReason records the stop reason and closes all open blocks.
func (d *oaicDecoder) finishReason(reason string) bool {
	switch reason {
	case "stop":
		d.stop = StopEnd
	case "length":
		d.stop = StopLength
	case "tool_calls":
		d.stop = StopToolUse
	case "content_filter":
		d.filtered = true
	}
	return d.closeBlocks()
}

// closeBlocks emits *_end for every open block, in block-index order.
func (d *oaicDecoder) closeBlocks() bool {
	if d.closed {
		return true
	}
	d.closed = true
	var ends []Event
	if d.textIdx >= 0 {
		ends = append(ends, Event{Type: EventTextEnd, Index: d.textIdx})
	}
	for _, st := range d.tools {
		ends = append(ends, Event{
			Type:  EventToolCallEnd,
			Index: st.blockIdx,
			Block: &Block{Type: BlockToolCall, ID: st.id, Name: st.name},
		})
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].Index < ends[j].Index })
	for _, ev := range ends {
		if !d.emit(ev) {
			return false
		}
	}
	return true
}

// finish emits the terminal event at end of stream ([DONE] or EOF).
func (d *oaicDecoder) finish() {
	if !d.closeBlocks() {
		return
	}
	if d.filtered {
		d.emit(ErrorEvent(errors.New("llm: openai-chat: content filtered"), d.usage))
		return
	}
	stop := d.stop
	if stop == "" {
		stop = StopEnd
	}
	d.emit(DoneEvent(stop, d.usage))
}

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

// streamOpenAIResponses implements the openai-responses protocol (SPEC §8.3).
// It receives the normalized request (§5.3 applied, MaxTokens defaulted) and
// emits raw content events plus exactly one terminal event per R6/R10.
func (c *Client) streamOpenAIResponses(ctx context.Context, req Request, apiKey string, emit func(Event) bool) {
	header := http.Header{}
	if apiKey != NoAuth {
		header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range req.Model.Headers {
		header.Set(k, v)
	}
	url := strings.TrimSuffix(req.Model.BaseURL, "/") + "/responses"
	resp, err := c.doJSON(ctx, http.MethodPost, url, header, oairBuildBody(req))
	if err != nil {
		emit(ErrorEvent(err, Usage{}))
		return
	}
	defer resp.Body.Close()
	oairDecode(ctx, resp.Body, emit)
}

// ---- request encoding -------------------------------------------------------

// oairBody is the /responses request payload (SPEC §8.3).
type oairBody struct {
	Model           string      `json:"model"`
	Instructions    string      `json:"instructions,omitempty"`
	Input           []any       `json:"input"`
	Tools           []oairTool  `json:"tools,omitempty"`
	Reasoning       *oairEffort `json:"reasoning,omitempty"`
	MaxOutputTokens int         `json:"max_output_tokens,omitempty"`
	Temperature     *float64    `json:"temperature,omitempty"`
	Stream          bool        `json:"stream"`
	Store           bool        `json:"store"`
	Include         []string    `json:"include"`
}

type oairTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type oairEffort struct {
	Effort string `json:"effort"`
}

// oairSig is the Block.Signature payload for reasoning items: opaque replay
// metadata carried between turns (SPEC §3.2).
type oairSig struct {
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content"`
}

func oairBuildBody(req Request) *oairBody {
	body := &oairBody{
		Model:           req.Model.ID,
		Instructions:    req.System,
		Input:           oairBuildInput(req.Messages),
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		Stream:          true,
		Store:           false,
		Include:         []string{"reasoning.encrypted_content"},
	}
	if req.Reasoning != EffortOff && req.Model.Reasoning {
		body.Reasoning = &oairEffort{Effort: string(req.Reasoning)}
	}
	for _, t := range req.Tools {
		params := t.Schema
		if len(params) == 0 {
			params = json.RawMessage("{}")
		}
		body.Tools = append(body.Tools, oairTool{
			Type: "function", Name: t.Name, Description: t.Description,
			Parameters: params, Strict: false,
		})
	}
	return body
}

func oairBuildInput(msgs []Message) []any {
	input := []any{}
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			if parts := oairUserParts(m.Blocks); len(parts) > 0 {
				input = append(input, oairMessageItem("user", parts))
			}

		case RoleAssistant:
			// Assistant history replays as per-block output items, in order.
			for _, b := range m.Blocks {
				switch b.Type {
				case BlockText:
					input = append(input, oairMessageItem("assistant", []any{
						map[string]any{"type": "output_text", "text": b.Text},
					}))
				case BlockThinking:
					// Same-model thinking replays natively from the Signature
					// JSON; cross-model thinking was already textified (R17).
					// Empty or unparseable signatures are dropped.
					var sig oairSig
					if b.Signature == "" || json.Unmarshal([]byte(b.Signature), &sig) != nil {
						continue
					}
					if sig.ID == "" && sig.EncryptedContent == "" {
						continue
					}
					// summary is required on a replayed reasoning item even
					// when empty — omitting it fails the whole request with
					// "Missing required parameter: 'input[N].summary'". The
					// reasoning travels in encrypted_content; summary is the
					// human-readable digest, which we never ask for.
					item := map[string]any{
						"type":              "reasoning",
						"encrypted_content": sig.EncryptedContent,
						"summary":           []any{},
					}
					if sig.ID != "" {
						item["id"] = sig.ID
					}
					input = append(input, item)
				case BlockToolCall:
					args := "{}"
					if len(b.Args) > 0 {
						args = string(b.Args)
					}
					input = append(input, map[string]any{
						"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": args,
					})
				}
			}

		case RoleToolResult:
			var text strings.Builder
			var images []any
			for _, b := range m.Blocks {
				switch b.Type {
				case BlockText:
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(b.Text)
				case BlockImage:
					images = append(images, oairImagePart(b))
				}
			}
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": text.String(),
			})
			// function_call_output cannot carry images; they spill over into
			// an immediately following user message (SPEC §8.3, as in §8.2).
			if len(images) > 0 {
				input = append(input, oairMessageItem("user", images))
			}
		}
	}
	return input
}

func oairUserParts(blocks []Block) []any {
	var parts []any
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
		case BlockImage:
			parts = append(parts, oairImagePart(b))
		}
	}
	return parts
}

func oairImagePart(b Block) map[string]any {
	return map[string]any{
		"type":      "input_image",
		"image_url": "data:" + b.MimeType + ";base64," + b.Data,
	}
}

func oairMessageItem(role string, content []any) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": content}
}

// ---- SSE decoding -----------------------------------------------------------

// oairFrame is the union of the stream event payloads we consume.
type oairFrame struct {
	Type        string        `json:"type"`
	OutputIndex *int          `json:"output_index"`
	ItemID      string        `json:"item_id"`
	Delta       string        `json:"delta"`
	Item        *oairItem     `json:"item"`
	Response    *oairResponse `json:"response"`
	// error event fields (top-level or nested)
	Message string     `json:"message"`
	Code    string     `json:"code"`
	Error   *oairError `json:"error"`
}

type oairItem struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	CallID           string            `json:"call_id"`
	Name             string            `json:"name"`
	Arguments        string            `json:"arguments"`
	EncryptedContent string            `json:"encrypted_content"`
	Summary          []oairSummaryPart `json:"summary"`
	Content          []oairContentPart `json:"content"`
}

type oairSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type oairContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type oairResponse struct {
	Usage             *oairUsage      `json:"usage"`
	IncompleteDetails *oairIncomplete `json:"incomplete_details"`
	Error             *oairError      `json:"error"`
}

type oairUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type oairIncomplete struct {
	Reason string `json:"reason"`
}

type oairError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// oairErrText keeps the provider's error code in the message. This protocol
// reports some failures as an SSE error event inside an HTTP 200 — quota
// exhaustion is one — so there is no status for the caller to classify on,
// and the code is the only machine-readable signal left.
func oairErrText(msg, code string) string {
	if code == "" {
		return msg
	}
	return msg + " (code: " + code + ")"
}

// oairDecoder tracks the provider's output items → our sequential content
// block indices (assigned in order of output_item.added).
type oairDecoder struct {
	nextIndex   int
	byItem      map[string]int
	byOutput    map[int]int
	types       map[int]BlockType
	sawToolCall bool
	usage       Usage
}

// oairHandledEvents lists the SSE event names we decode; a malformed data
// payload on one of these is a terminal stream error (R8). Everything else
// (response.in_progress, content_part events, ...) is skipped.
var oairHandledEvents = map[string]bool{
	"response.created":                       true,
	"response.output_item.added":             true,
	"response.output_text.delta":             true,
	"response.reasoning_summary_text.delta":  true,
	"response.function_call_arguments.delta": true,
	"response.output_item.done":              true,
	"response.completed":                     true,
	"response.incomplete":                    true,
	"response.failed":                        true,
	"error":                                  true,
}

func oairDecode(ctx context.Context, body io.Reader, emit func(Event) bool) {
	d := &oairDecoder{byItem: map[string]int{}, byOutput: map[int]int{}, types: map[int]BlockType{}}
	r := sse.NewReader(body)
	for {
		ev, err := r.Next()
		if err != nil {
			if err == io.EOF {
				emit(ErrorEvent(errors.New("llm: openai-responses: stream ended before response.completed"), d.usage))
			} else {
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = ctxErr
				}
				emit(ErrorEvent(fmt.Errorf("llm: openai-responses: read stream: %w", err), d.usage))
			}
			return
		}
		if strings.TrimSpace(ev.Data) == "[DONE]" {
			continue
		}
		var frame oairFrame
		if jerr := json.Unmarshal([]byte(ev.Data), &frame); jerr != nil {
			if oairHandledEvents[ev.Name] {
				emit(ErrorEvent(fmt.Errorf("llm: openai-responses: decode %s event: %w", ev.Name, jerr), d.usage))
				return
			}
			continue
		}
		name := ev.Name
		if name == "" {
			name = frame.Type
		}
		cont, terminal := d.handle(name, &frame, emit)
		if terminal || !cont {
			return
		}
	}
}

// handle processes one decoded frame. It returns cont=false when the consumer
// stopped, and terminal=true after emitting the terminal event.
func (d *oairDecoder) handle(name string, frame *oairFrame, emit func(Event) bool) (cont, terminal bool) {
	switch name {
	case "response.created":
		// EventStart was already emitted by the client before dispatch.

	case "response.output_item.added":
		if frame.Item == nil {
			break
		}
		var t BlockType
		switch frame.Item.Type {
		case "message":
			t = BlockText
		case "reasoning":
			t = BlockThinking
		case "function_call":
			t = BlockToolCall
		default:
			return true, false // unknown item kinds get no content block
		}
		idx := d.open(frame.Item.ID, frame.OutputIndex, t)
		switch t {
		case BlockText:
			return emit(Event{Type: EventTextStart, Index: idx}), false
		case BlockThinking:
			return emit(Event{Type: EventThinkingStart, Index: idx}), false
		case BlockToolCall:
			d.sawToolCall = true
			return emit(Event{
				Type: EventToolCallStart, Index: idx,
				Block: &Block{Type: BlockToolCall, ID: frame.Item.CallID, Name: frame.Item.Name},
			}), false
		}

	case "response.output_text.delta":
		idx, ok := d.ensure(frame, BlockText, EventTextStart, emit)
		if !ok {
			return false, false
		}
		return emit(Event{Type: EventTextDelta, Index: idx, Delta: frame.Delta}), false

	case "response.reasoning_summary_text.delta":
		idx, ok := d.ensure(frame, BlockThinking, EventThinkingStart, emit)
		if !ok {
			return false, false
		}
		return emit(Event{Type: EventThinkingDelta, Index: idx, Delta: frame.Delta}), false

	case "response.function_call_arguments.delta":
		idx, ok := d.ensure(frame, BlockToolCall, EventToolCallStart, emit)
		if !ok {
			return false, false
		}
		return emit(Event{Type: EventToolCallDelta, Index: idx, Delta: frame.Delta}), false

	case "response.output_item.done":
		if frame.Item == nil {
			break
		}
		idx, ok := d.lookup(frame.Item.ID, frame.OutputIndex)
		if !ok {
			break // item kind we never opened
		}
		return d.emitEnd(idx, frame.Item, emit), false

	case "response.completed", "response.incomplete":
		if frame.Response != nil && frame.Response.Usage != nil {
			u := frame.Response.Usage
			d.usage = Usage{Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.InputTokensDetails.CachedTokens}
		}
		stop := StopEnd
		if d.sawToolCall {
			stop = StopToolUse
		}
		if frame.Response != nil && frame.Response.IncompleteDetails != nil &&
			frame.Response.IncompleteDetails.Reason == "max_output_tokens" {
			stop = StopLength
		}
		emit(DoneEvent(stop, d.usage))
		return false, true

	case "response.failed":
		msg := "response failed"
		var code string
		if frame.Response != nil && frame.Response.Error != nil {
			if frame.Response.Error.Message != "" {
				msg = frame.Response.Error.Message
			}
			code = frame.Response.Error.Code
		}
		emit(ErrorEvent(fmt.Errorf("llm: openai-responses: %s", oairErrText(msg, code)), d.usage))
		return false, true

	case "error":
		// The code arrives top-level on the SSE error event and nested inside
		// "error" on a failed-response envelope; take whichever is present.
		msg, code := frame.Message, frame.Code
		if frame.Error != nil {
			if msg == "" {
				msg = frame.Error.Message
			}
			if code == "" {
				code = frame.Error.Code
			}
		}
		if msg == "" {
			msg = "provider error"
		}
		emit(ErrorEvent(fmt.Errorf("llm: openai-responses: %s", oairErrText(msg, code)), d.usage))
		return false, true
	}
	return true, false
}

// emitEnd closes block idx with the authoritative fields from the done item.
func (d *oairDecoder) emitEnd(idx int, item *oairItem, emit func(Event) bool) bool {
	switch d.types[idx] {
	case BlockText:
		var text strings.Builder
		for _, p := range item.Content {
			if p.Type == "output_text" {
				text.WriteString(p.Text)
			}
		}
		var blk *Block
		if text.Len() > 0 {
			blk = &Block{Type: BlockText, Text: text.String()}
		}
		return emit(Event{Type: EventTextEnd, Index: idx, Block: blk})

	case BlockThinking:
		blk := &Block{Type: BlockThinking}
		if item.ID != "" || item.EncryptedContent != "" {
			if sig, err := json.Marshal(oairSig{ID: item.ID, EncryptedContent: item.EncryptedContent}); err == nil {
				blk.Signature = string(sig)
			}
		}
		var parts []string
		for _, p := range item.Summary {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		blk.Text = strings.Join(parts, "\n")
		return emit(Event{Type: EventThinkingEnd, Index: idx, Block: blk})

	case BlockToolCall:
		blk := &Block{Type: BlockToolCall, ID: item.CallID, Name: item.Name}
		if item.Arguments != "" && json.Valid([]byte(item.Arguments)) {
			blk.Args = json.RawMessage(item.Arguments)
		}
		return emit(Event{Type: EventToolCallEnd, Index: idx, Block: blk})
	}
	return true
}

// ensure returns the block index for a delta frame, lazily opening (and
// emitting the *_start for) a block if the provider skipped output_item.added.
func (d *oairDecoder) ensure(frame *oairFrame, t BlockType, start EventType, emit func(Event) bool) (int, bool) {
	if idx, ok := d.lookup(frame.ItemID, frame.OutputIndex); ok {
		return idx, true
	}
	idx := d.open(frame.ItemID, frame.OutputIndex, t)
	ev := Event{Type: start, Index: idx}
	if t == BlockToolCall {
		d.sawToolCall = true
		ev.Block = &Block{Type: BlockToolCall}
	}
	return idx, emit(ev)
}

func (d *oairDecoder) lookup(itemID string, outputIndex *int) (int, bool) {
	if itemID != "" {
		if idx, ok := d.byItem[itemID]; ok {
			return idx, true
		}
	}
	if outputIndex != nil {
		if idx, ok := d.byOutput[*outputIndex]; ok {
			return idx, true
		}
	}
	return 0, false
}

func (d *oairDecoder) open(itemID string, outputIndex *int, t BlockType) int {
	idx := d.nextIndex
	d.nextIndex++
	if itemID != "" {
		d.byItem[itemID] = idx
	}
	if outputIndex != nil {
		d.byOutput[*outputIndex] = idx
	}
	d.types[idx] = t
	return idx
}

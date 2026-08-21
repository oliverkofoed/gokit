package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/oliverkofoed/gokit/agentkit/llm/internal/sse"
)

// streamGemini implements the google-gemini protocol (SPEC §8.4). It builds
// the streamGenerateContent payload from the (already normalized) request,
// issues it through the client transport, and decodes the SSE stream into raw
// events for the NewStream accumulator. EventStart has already been emitted
// by the caller; this function emits content events plus exactly one
// DoneEvent/ErrorEvent (R6, R8).
func (c *Client) streamGemini(ctx context.Context, req Request, apiKey string, emit func(Event) bool) {
	url := strings.TrimSuffix(req.Model.BaseURL, "/") +
		"/v1beta/models/" + req.Model.ID + ":streamGenerateContent?alt=sse"

	header := http.Header{}
	if apiKey != NoAuth {
		header.Set("x-goog-api-key", apiKey)
	}
	for k, v := range req.Model.Headers {
		header.Set(k, v)
	}

	resp, err := c.doJSON(ctx, http.MethodPost, url, header, gemBuildRequest(c, req))
	if err != nil {
		emit(ErrorEvent(err, Usage{}))
		return
	}
	defer resp.Body.Close()

	gemDecodeSSE(resp.Body, emit)
}

// ---- request payload --------------------------------------------------------

type gemRequest struct {
	SystemInstruction *gemContent  `json:"systemInstruction,omitempty"`
	Contents          []gemContent `json:"contents"`
	Tools             []gemTool    `json:"tools,omitempty"`
	GenerationConfig  gemGenConfig `json:"generationConfig"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"` // "user" / "model"; absent on systemInstruction
	Parts []gemPart `json:"parts"`
}

type gemPart struct {
	Text             string       `json:"text,omitempty"`
	Thought          bool         `json:"thought,omitempty"`
	ThoughtSignature string       `json:"thoughtSignature,omitempty"`
	InlineData       *gemBlob     `json:"inlineData,omitempty"`
	FunctionCall     *gemFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *gemFuncResp `json:"functionResponse,omitempty"`
}

type gemBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemFuncCall struct {
	ID   string          `json:"id,omitempty"` // 3.x issues one; earlier models omit it
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gemFuncResp struct {
	ID       string         `json:"id,omitempty"` // echoes the call's id when there was one
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gemTool struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations"`
}

type gemFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type gemGenConfig struct {
	MaxOutputTokens int                `json:"maxOutputTokens,omitempty"`
	Temperature     *float64           `json:"temperature,omitempty"`
	ThinkingConfig  *gemThinkingConfig `json:"thinkingConfig,omitempty"`
}

// gemThinkingConfig carries whichever reasoning control the model generation
// uses. ThinkingBudget (<=2.5) and ThinkingLevel (3.x) are mutually
// exclusive: sending both is a 400, hence omitempty on each.
type gemThinkingConfig struct {
	ThinkingBudget  int    `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts"`
}

// gemBuildRequest maps a normalized Request to the Gemini wire payload.
func gemBuildRequest(c *Client, req Request) gemRequest {
	out := gemRequest{
		Contents: gemContents(req.Messages),
		GenerationConfig: gemGenConfig{
			MaxOutputTokens: req.MaxTokens,
		},
	}
	// Gemini 3.x deprecated the sampling parameters; sending them is ignored
	// today and is documented to become an error.
	if !req.Model.Quirks.GeminiV3 {
		out.GenerationConfig.Temperature = req.Temperature
	}
	if req.System != "" {
		out.SystemInstruction = &gemContent{Parts: []gemPart{{Text: req.System}}}
	}
	// R14: effort maps to a categorical level on 3.x, a token budget before.
	if req.Reasoning != "" && req.Model.Reasoning {
		tc := &gemThinkingConfig{IncludeThoughts: true}
		if req.Model.Quirks.GeminiV3 {
			tc.ThinkingLevel = string(req.Reasoning)
		} else {
			tc.ThinkingBudget = c.thinkingBudget(req.Reasoning)
		}
		out.GenerationConfig.ThinkingConfig = tc
	}
	if len(req.Tools) > 0 {
		decls := make([]gemFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, gemFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  gemSanitizeSchema(t.Schema),
			})
		}
		out.Tools = []gemTool{{FunctionDeclarations: decls}}
	}
	return out
}

// gemHasData reports whether a part carries one of the Part "data" oneof
// members. A part with none fails the entire request with
// "required oneof field 'data' must have one initialized field", so every
// assembled part is filtered through this before it goes out.
func gemHasData(p gemPart) bool {
	return p.Text != "" || p.InlineData != nil || p.FunctionCall != nil || p.FunctionResponse != nil
}

func gemValidParts(parts []gemPart) []gemPart {
	out := parts[:0]
	for _, p := range parts {
		if gemHasData(p) {
			out = append(out, p)
		}
	}
	return out
}

// gemContents maps normalized messages to Gemini contents. Consecutive tool
// result messages merge into one user-role content, so parallel function
// calls are answered by matching functionResponse parts in a single turn.
func gemContents(msgs []Message) []gemContent {
	contents := []gemContent{}
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case RoleUser:
			if parts := gemValidParts(gemUserParts(m)); len(parts) > 0 {
				contents = append(contents, gemContent{Role: "user", Parts: parts})
			}
		case RoleAssistant:
			if parts := gemValidParts(gemAssistantParts(m)); len(parts) > 0 {
				contents = append(contents, gemContent{Role: "model", Parts: parts})
			}
		case RoleToolResult:
			var parts []gemPart
			for {
				parts = append(parts, gemToolResultParts(msgs[i])...)
				if i+1 >= len(msgs) || msgs[i+1].Role != RoleToolResult {
					break
				}
				i++
			}
			if parts = gemValidParts(parts); len(parts) > 0 {
				contents = append(contents, gemContent{Role: "user", Parts: parts})
			}
		}
	}
	return contents
}

func gemUserParts(m Message) []gemPart {
	var parts []gemPart
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, gemPart{Text: b.Text})
		case BlockImage:
			parts = append(parts, gemPart{InlineData: &gemBlob{MimeType: b.MimeType, Data: b.Data}})
		}
	}
	return parts
}

func gemAssistantParts(m Message) []gemPart {
	var parts []gemPart
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, gemPart{Text: b.Text, ThoughtSignature: b.Signature})
		case BlockThinking:
			// Only same-model thinking survives normalization (R17); Gemini
			// has no redacted-thinking representation, so redacted blocks
			// are dropped.
			if b.Redacted {
				continue
			}
			parts = append(parts, gemPart{Text: b.Text, Thought: true, ThoughtSignature: b.Signature})
		case BlockToolCall:
			// 3.x requires the thought signature back on the call it came
			// from, or it rejects the turn outright.
			parts = append(parts, gemPart{
				FunctionCall:     &gemFuncCall{ID: b.ID, Name: b.Name, Args: gemArgsObject(b.Args)},
				ThoughtSignature: b.Signature,
			})
		}
	}
	return parts
}

// gemArgsObject returns args as a valid JSON value for the wire, defaulting
// to an empty object.
func gemArgsObject(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || !json.Valid(args) {
		return json.RawMessage("{}")
	}
	return args
}

// gemToolResultParts maps one tool result message: a functionResponse part
// carrying the concatenated text, plus one inlineData part per image block.
func gemToolResultParts(m Message) []gemPart {
	var texts []string
	for _, b := range m.Blocks {
		if b.Type == BlockText {
			texts = append(texts, b.Text)
		}
	}
	parts := []gemPart{{FunctionResponse: &gemFuncResp{
		ID:       m.ToolCallID,
		Name:     m.ToolName,
		Response: map[string]any{"output": strings.Join(texts, "\n")},
	}}}
	for _, b := range m.Blocks {
		if b.Type == BlockImage {
			parts = append(parts, gemPart{InlineData: &gemBlob{MimeType: b.MimeType, Data: b.Data}})
		}
	}
	return parts
}

// ---- schema sanitization ------------------------------------------------------

// gemSanitizeSchema walks a JSON Schema tree and strips constructs Gemini's
// functionDeclarations endpoint rejects: `additionalProperties` and `$schema`
// everywhere, `format` unless its value is "enum" or "date-time", and `enum`
// on non-string-typed schemas. Unparseable input is passed through verbatim
// (the provider will report the real error).
func gemSanitizeSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(schema, &v); err != nil {
		return schema
	}
	gemSanitizeValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return schema
	}
	return out
}

// gemSanitizeValue recursively sanitizes schema objects. The walk is
// schema-aware just enough not to strip user property names: the immediate
// children of "properties" (and friends) are schemas, but the map itself is
// a name→schema container whose keys must survive untouched.
func gemSanitizeValue(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				gemSanitizeValue(e)
			}
		}
		return
	}
	delete(m, "additionalProperties")
	delete(m, "$schema")
	if f, ok := m["format"]; ok {
		if fs, isStr := f.(string); !isStr || (fs != "enum" && fs != "date-time") {
			delete(m, "format")
		}
	}
	if _, ok := m["enum"]; ok {
		if typ, _ := m["type"].(string); typ != "string" {
			delete(m, "enum")
		}
	}
	for k, child := range m {
		switch k {
		case "properties", "patternProperties", "$defs", "definitions":
			if props, ok := child.(map[string]any); ok {
				for _, sub := range props {
					gemSanitizeValue(sub)
				}
			}
		case "items", "prefixItems", "contains", "not", "if", "then", "else",
			"anyOf", "oneOf", "allOf":
			gemSanitizeValue(child)
		}
	}
}

// ---- SSE decode ---------------------------------------------------------------

// gemResponse is one streamed GenerateContentResponse.
type gemResponse struct {
	Candidates []struct {
		Content struct {
			Parts []gemRespPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

type gemRespPart struct {
	Text             string       `json:"text"`
	Thought          bool         `json:"thought"`
	ThoughtSignature string       `json:"thoughtSignature"`
	FunctionCall     *gemFuncCall `json:"functionCall"`
}

// gemDecoder tracks open-block state while folding streamed parts into raw
// events. Consecutive parts of the same kind continue one block; a different
// part kind closes the current block and opens the next index.
type gemDecoder struct {
	emit      func(Event) bool
	nextIndex int
	openType  BlockType // "" when no block is open
	openIndex int
	openSig   string // last thoughtSignature seen for the open thinking block
	sawCall   bool
	finish    string
	usage     Usage
}

// gemDecodeSSE consumes the SSE body and emits content events plus one
// terminal event. Any emit returning false aborts promptly without a terminal
// event (NewStream synthesizes the consumer-stop error).
func gemDecodeSSE(body io.Reader, emit func(Event) bool) {
	d := &gemDecoder{emit: emit}
	r := sse.NewReader(body)
	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !d.closeOpen() {
				return
			}
			emit(ErrorEvent(err, d.usage))
			return
		}
		data := strings.TrimSpace(ev.Data)
		if data == "" || data == "[DONE]" {
			continue
		}
		ok, terminal := d.apply(data)
		if !ok || terminal {
			return
		}
	}
	if !d.closeOpen() {
		return
	}
	switch d.finish {
	case "", "STOP":
		stop := StopEnd
		if d.sawCall {
			stop = StopToolUse
		}
		emit(DoneEvent(stop, d.usage))
	case "MAX_TOKENS":
		emit(DoneEvent(StopLength, d.usage))
	default:
		emit(ErrorEvent(fmt.Errorf("gemini: finish reason %s", d.finish), d.usage))
	}
}

// apply folds one GenerateContentResponse. ok=false means the consumer
// stopped; terminal=true means a terminal event was emitted.
func (d *gemDecoder) apply(data string) (ok, terminal bool) {
	var gr gemResponse
	if err := json.Unmarshal([]byte(data), &gr); err != nil {
		if !d.closeOpen() {
			return false, false
		}
		d.emit(ErrorEvent(fmt.Errorf("gemini: decode stream response: %w", err), d.usage))
		return true, true
	}
	if gr.UsageMetadata != nil {
		d.usage.Input = gr.UsageMetadata.PromptTokenCount
		d.usage.Output = gr.UsageMetadata.CandidatesTokenCount + gr.UsageMetadata.ThoughtsTokenCount
		d.usage.CacheRead = gr.UsageMetadata.CachedContentTokenCount
	}
	if len(gr.Candidates) == 0 {
		if gr.PromptFeedback != nil && gr.PromptFeedback.BlockReason != "" {
			if !d.closeOpen() {
				return false, false
			}
			d.emit(ErrorEvent(fmt.Errorf("gemini: prompt blocked: %s", gr.PromptFeedback.BlockReason), d.usage))
			return true, true
		}
		return true, false
	}
	cand := gr.Candidates[0]
	for _, p := range cand.Content.Parts {
		if !d.applyPart(p) {
			return false, false
		}
	}
	if cand.FinishReason != "" {
		d.finish = cand.FinishReason
	}
	return true, false
}

func (d *gemDecoder) applyPart(p gemRespPart) bool {
	switch {
	case p.FunctionCall != nil:
		// Function calls arrive whole: start + one delta with the full args
		// JSON + end. Gemini sends no call id — the block keeps ID "" and
		// normalization synthesizes call_<n> on replay (R18).
		if !d.closeOpen() {
			return false
		}
		d.sawCall = true
		idx := d.nextIndex
		d.nextIndex++
		if !d.emit(Event{Type: EventToolCallStart, Index: idx,
			Block: &Block{
				Type: BlockToolCall, ID: p.FunctionCall.ID, Name: p.FunctionCall.Name,
				// 3.x attaches the thought signature here; it must be sent
				// back on replay.
				Signature: p.ThoughtSignature,
			}}) {
			return false
		}
		args := string(gemArgsObject(p.FunctionCall.Args))
		if !d.emit(Event{Type: EventToolCallDelta, Index: idx, Delta: args}) {
			return false
		}
		return d.emit(Event{Type: EventToolCallEnd, Index: idx})

	case p.Thought:
		if d.openType != BlockThinking {
			if !d.closeOpen() {
				return false
			}
			d.openType = BlockThinking
			d.openIndex = d.nextIndex
			d.nextIndex++
			if !d.emit(Event{Type: EventThinkingStart, Index: d.openIndex}) {
				return false
			}
		}
		if p.ThoughtSignature != "" {
			d.openSig = p.ThoughtSignature
		}
		return d.emit(Event{Type: EventThinkingDelta, Index: d.openIndex, Delta: p.Text})

	default:
		// 3.x ends a turn with a {"text":""} part that carries only the
		// thought signature, or nothing at all. Opening a block for it would
		// re-encode as a Part with no data member and fail the next request,
		// so fold any signature into the open block and drop the part.
		if p.Text == "" {
			if p.ThoughtSignature != "" {
				d.openSig = p.ThoughtSignature
			}
			return true
		}
		if d.openType != BlockText {
			if !d.closeOpen() {
				return false
			}
			d.openType = BlockText
			d.openIndex = d.nextIndex
			d.nextIndex++
			if !d.emit(Event{Type: EventTextStart, Index: d.openIndex}) {
				return false
			}
		}
		return d.emit(Event{Type: EventTextDelta, Index: d.openIndex, Delta: p.Text})
	}
}

// closeOpen ends the currently open text/thinking block, attaching the
// captured thought signature via the end event's Block override.
func (d *gemDecoder) closeOpen() bool {
	if d.openType == "" {
		return true
	}
	ev := Event{Index: d.openIndex}
	if d.openType == BlockThinking {
		ev.Type = EventThinkingEnd
	} else {
		ev.Type = EventTextEnd
	}
	// 3.x returns the thought signature on the content part rather than on a
	// separate thought part, so it has to survive on whichever block was open.
	if d.openSig != "" {
		ev.Block = &Block{Signature: d.openSig}
	}
	d.openType = ""
	d.openSig = ""
	return d.emit(ev)
}

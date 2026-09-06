package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/cassette"
)

// gemModel is the model used by Gemini wire tests.
func gemModel() Model {
	return Model{
		ID: "gemini-test", API: GoogleGemini, Provider: "google",
		BaseURL:       "https://gem.example.com",
		Cost:          Cost{Input: 1, Output: 2, CacheRead: 0.5},
		ContextWindow: 100_000, MaxOutput: 4096,
		Reasoning: true, Vision: true, Documents: true,
		Headers: map[string]string{"X-Gem-Extra": "extra-v"},
	}
}

// gemClient builds a client over the given transport with a static test key.
func gemClient(ct *captureTransport) *Client {
	return New(WithTransport(ct), WithAPIKey("google", "gem-test-key"))
}

// gemData wraps one GenerateContentResponse JSON as an SSE chunk.
func gemData(data string) string { return sseChunk("", data) }

// gemPath digs into a decoded JSON body by key/index path.
func gemPath(tb testing.TB, v any, path ...any) any {
	tb.Helper()
	for _, p := range path {
		switch key := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				tb.Fatalf("gemPath %v: not an object at %q (got %T)", path, key, v)
			}
			v, ok = m[key]
			if !ok {
				tb.Fatalf("gemPath %v: missing key %q in %v", path, key, m)
			}
		case int:
			arr, ok := v.([]any)
			if !ok || key >= len(arr) {
				tb.Fatalf("gemPath %v: no index %d (got %T)", path, key, v)
			}
			v = arr[key]
		}
	}
	return v
}

func TestGeminiBasicText(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"Hello "}]}}]}`),
		gemData(`{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"cachedContentTokenCount":3}}`),
	}}
	temp := 0.5
	s := gemClient(ct).Stream(context.Background(), Request{
		Model:       gemModel(),
		System:      "be nice",
		Messages:    []Message{UserText("hi")},
		Temperature: &temp,
	})
	events := collectEvents(s)

	// Payload golden.
	req := ct.lastReq(t)
	if req.Method != "POST" {
		t.Fatalf("method = %q", req.Method)
	}
	wantURL := "https://gem.example.com/v1beta/models/gemini-test:streamGenerateContent?alt=sse"
	if req.URL != wantURL {
		t.Fatalf("url = %q, want %q", req.URL, wantURL)
	}
	if got := req.Header.Get("x-goog-api-key"); got != "gem-test-key" {
		t.Fatalf("x-goog-api-key = %q", got)
	}
	if got := req.Header.Get("X-Gem-Extra"); got != "extra-v" {
		t.Fatalf("model extra header = %q", got)
	}
	body := ct.lastBody(t)
	if got := gemPath(t, body, "systemInstruction", "parts", 0, "text"); got != "be nice" {
		t.Fatalf("systemInstruction = %v", got)
	}
	if got := gemPath(t, body, "generationConfig", "maxOutputTokens"); got != float64(4096) {
		t.Fatalf("maxOutputTokens = %v, want 4096 (Model.MaxOutput default)", got)
	}
	if got := gemPath(t, body, "generationConfig", "temperature"); got != 0.5 {
		t.Fatalf("temperature = %v", got)
	}
	if _, ok := gemPath(t, body, "generationConfig").(map[string]any)["thinkingConfig"]; ok {
		t.Fatal("thinkingConfig present without Reasoning")
	}
	if _, ok := body["tools"]; ok {
		t.Fatal("tools present without ToolDefs")
	}
	if got := gemPath(t, body, "contents", 0, "role"); got != "user" {
		t.Fatalf("contents[0].role = %v", got)
	}
	if got := gemPath(t, body, "contents", 0, "parts", 0, "text"); got != "hi" {
		t.Fatalf("contents[0].parts[0].text = %v", got)
	}

	// Event sequence + final message.
	want := []EventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventDone}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if textOf(msg) != "Hello world" {
		t.Fatalf("text = %q", textOf(msg))
	}
	if msg.StopReason != StopEnd {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	u := msg.Usage
	if u == nil || u.Input != 10 || u.Output != 5 || u.CacheRead != 3 {
		t.Fatalf("usage = %+v", u)
	}
	if wantCost := (10*1.0 + 5*2.0 + 3*0.5) / 1e6; u.TotalCost != wantCost {
		t.Fatalf("cost = %v, want %v", u.TotalCost, wantCost)
	}
}

func TestGeminiNoAuth(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`),
	}}
	c := New(WithTransport(ct), WithAPIKey("google", NoAuth))
	if _, err := c.Complete(context.Background(), Request{Model: gemModel(), Messages: []Message{UserText("hi")}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if vals, ok := ct.lastReq(t).Header["X-Goog-Api-Key"]; ok {
		t.Fatalf("x-goog-api-key sent under NoAuth: %v", vals)
	}
}

func TestGeminiToolCall(t *testing.T) {
	schema := json.RawMessage(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"path":  {"type": "string", "format": "uri"},
			"count": {"type": "integer", "format": "int32", "enum": [1, 2]},
			"mode":  {"type": "string", "enum": ["a", "b"]},
			"when":  {"type": "string", "format": "date-time"},
			"nested": {
				"type": "object",
				"additionalProperties": false,
				"properties": {"x": {"type": "string", "format": "uuid"}}
			}
		},
		"required": ["path"]
	}`)
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file","args":{"path":"main.go","count":2}}}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":4}}`),
	}}
	s := gemClient(ct).Stream(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("read it")},
		Tools:    []ToolDef{{Name: "read_file", Description: "reads a file", Schema: schema}},
	})
	events := collectEvents(s)

	// functionDeclarations encoding with sanitized schema.
	body := ct.lastBody(t)
	decl := gemPath(t, body, "tools", 0, "functionDeclarations", 0).(map[string]any)
	if decl["name"] != "read_file" || decl["description"] != "reads a file" {
		t.Fatalf("decl = %v", decl)
	}
	params := decl["parameters"].(map[string]any)
	if _, ok := params["$schema"]; ok {
		t.Fatal("$schema not stripped")
	}
	if _, ok := params["additionalProperties"]; ok {
		t.Fatal("additionalProperties not stripped")
	}
	props := params["properties"].(map[string]any)
	if _, ok := props["path"].(map[string]any)["format"]; ok {
		t.Fatal(`format "uri" not stripped`)
	}
	count := props["count"].(map[string]any)
	if _, ok := count["format"]; ok {
		t.Fatal(`format "int32" not stripped`)
	}
	if _, ok := count["enum"]; ok {
		t.Fatal("enum on non-string schema not stripped")
	}
	if _, ok := props["mode"].(map[string]any)["enum"]; !ok {
		t.Fatal("enum on string schema wrongly stripped")
	}
	if got := props["when"].(map[string]any)["format"]; got != "date-time" {
		t.Fatalf(`format date-time = %v, want kept`, got)
	}
	nested := props["nested"].(map[string]any)
	if _, ok := nested["additionalProperties"]; ok {
		t.Fatal("nested additionalProperties not stripped")
	}
	if _, ok := nested["properties"].(map[string]any)["x"].(map[string]any)["format"]; ok {
		t.Fatal("nested format not stripped")
	}
	if !reflect.DeepEqual(params["required"], []any{"path"}) {
		t.Fatalf("required = %v", params["required"])
	}

	// Whole-args call: start + exactly one delta + end.
	want := []EventType{EventStart, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventDone}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want tool_use", msg.StopReason)
	}
	b := msg.Blocks[0]
	if b.Type != BlockToolCall || b.Name != "read_file" || b.ID != "" {
		t.Fatalf("tool block = %+v", b)
	}
	var args map[string]any
	if err := json.Unmarshal(b.Args, &args); err != nil {
		t.Fatalf("args: %v (%s)", err, b.Args)
	}
	if args["path"] != "main.go" || args["count"] != float64(2) {
		t.Fatalf("args = %v", args)
	}
}

func TestGeminiToolResultRoundtrip(t *testing.T) {
	assistant := Message{
		Role: RoleAssistant, Model: "gemini-test", Provider: "google", API: GoogleGemini,
		Blocks: []Block{{Type: BlockToolCall, ID: "call_1", Name: "read_file", Args: json.RawMessage(`{"path":"x"}`)}},
	}
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model: gemModel(),
		Messages: []Message{
			UserText("read it"),
			assistant,
			ToolResultMessage("call_1", "read_file", false, TextBlock("contents here")),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	if got := gemPath(t, body, "contents", 1, "role"); got != "model" {
		t.Fatalf("contents[1].role = %v", got)
	}
	fc := gemPath(t, body, "contents", 1, "parts", 0, "functionCall").(map[string]any)
	if fc["name"] != "read_file" {
		t.Fatalf("functionCall = %v", fc)
	}
	if got := gemPath(t, fc, "args", "path"); got != "x" {
		t.Fatalf("functionCall.args = %v", fc["args"])
	}
	if got := gemPath(t, body, "contents", 2, "role"); got != "user" {
		t.Fatalf("contents[2].role = %v", got)
	}
	fr := gemPath(t, body, "contents", 2, "parts", 0, "functionResponse").(map[string]any)
	if fr["name"] != "read_file" {
		t.Fatalf("functionResponse.name = %v", fr["name"])
	}
	if got := gemPath(t, fr, "response", "output"); got != "contents here" {
		t.Fatalf("functionResponse.response = %v", fr["response"])
	}
}

func TestGeminiThinking(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"pondering","thought":true},{"text":" more","thought":true,"thoughtSignature":"sig123"}]}}]}`),
		gemData(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":3,"thoughtsTokenCount":9}}`),
	}}
	s := gemClient(ct).Stream(context.Background(), Request{
		Model:     gemModel(),
		Messages:  []Message{UserText("think")},
		Reasoning: EffortMedium,
	})
	events := collectEvents(s)

	tc := gemPath(t, ct.lastBody(t), "generationConfig", "thinkingConfig").(map[string]any)
	if tc["thinkingBudget"] != float64(8192) {
		t.Fatalf("thinkingBudget = %v, want 8192 for medium", tc["thinkingBudget"])
	}
	if tc["includeThoughts"] != true {
		t.Fatalf("includeThoughts = %v", tc["includeThoughts"])
	}

	want := []EventType{EventStart,
		EventThinkingStart, EventThinkingDelta, EventThinkingDelta, EventThinkingEnd,
		EventTextStart, EventTextDelta, EventTextEnd, EventDone}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, ev := range events {
		if ev.Type == EventThinkingEnd && (ev.Block == nil || ev.Block.Signature != "sig123") {
			t.Fatalf("thinking_end block = %+v, want signature sig123", ev.Block)
		}
	}
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if b := msg.Blocks[0]; b.Type != BlockThinking || b.Text != "pondering more" || b.Signature != "sig123" {
		t.Fatalf("thinking block = %+v", b)
	}
	if b := msg.Blocks[1]; b.Type != BlockText || b.Text != "answer" {
		t.Fatalf("text block = %+v", b)
	}
	// thoughtsTokenCount counts inside Output.
	if u := msg.Usage; u.Input != 6 || u.Output != 3+9 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestGeminiThinkingReplay(t *testing.T) {
	assistant := Message{
		Role: RoleAssistant, Model: "gemini-test", Provider: "google", API: GoogleGemini,
		Blocks: []Block{
			{Type: BlockThinking, Text: "earlier thought", Signature: "sig-abc"},
			{Type: BlockThinking, Text: "unsigned thought"},
			{Type: BlockText, Text: "earlier answer"},
		},
	}
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model:     gemModel(),
		Messages:  []Message{UserText("q1"), assistant, UserText("q2")},
		Reasoning: EffortHigh,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	p0 := gemPath(t, body, "contents", 1, "parts", 0).(map[string]any)
	if p0["text"] != "earlier thought" || p0["thought"] != true || p0["thoughtSignature"] != "sig-abc" {
		t.Fatalf("same-model thinking part = %v", p0)
	}
	p1 := gemPath(t, body, "contents", 1, "parts", 1).(map[string]any)
	if p1["thought"] != true {
		t.Fatalf("unsigned thinking part = %v", p1)
	}
	if _, ok := p1["thoughtSignature"]; ok {
		t.Fatalf("empty thoughtSignature must be omitted: %v", p1)
	}
	p2 := gemPath(t, body, "contents", 1, "parts", 2).(map[string]any)
	if p2["text"] != "earlier answer" {
		t.Fatalf("text part = %v", p2)
	}
	if _, ok := p2["thought"]; ok {
		t.Fatalf("plain text part carries thought: %v", p2)
	}
}

func TestGeminiImageInput(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"a cat"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserBlocks(TextBlock("look"), ImageBlock("image/png", []byte{1, 2, 3}))},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	if got := gemPath(t, body, "contents", 0, "parts", 0, "text"); got != "look" {
		t.Fatalf("parts[0] = %v", got)
	}
	img := gemPath(t, body, "contents", 0, "parts", 1, "inlineData").(map[string]any)
	if img["mimeType"] != "image/png" {
		t.Fatalf("mimeType = %v", img["mimeType"])
	}
	if img["data"] != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("data = %v", img["data"])
	}
}

func TestGeminiImageInToolResult(t *testing.T) {
	assistant := Message{
		Role: RoleAssistant, Model: "gemini-test", Provider: "google", API: GoogleGemini,
		Blocks: []Block{{Type: BlockToolCall, ID: "call_1", Name: "screenshot", Args: json.RawMessage(`{}`)}},
	}
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"nice"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model: gemModel(),
		Messages: []Message{
			UserText("shoot"),
			assistant,
			ToolResultMessage("call_1", "screenshot", false,
				TextBlock("captured"), ImageBlock("image/jpeg", []byte{9, 8})),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	if got := gemPath(t, body, "contents", 2, "role"); got != "user" {
		t.Fatalf("contents[2].role = %v", got)
	}
	fr := gemPath(t, body, "contents", 2, "parts", 0, "functionResponse").(map[string]any)
	if got := gemPath(t, fr, "response", "output"); got != "captured" {
		t.Fatalf("output = %v", got)
	}
	img := gemPath(t, body, "contents", 2, "parts", 1, "inlineData").(map[string]any)
	if img["mimeType"] != "image/jpeg" || img["data"] != base64.StdEncoding.EncodeToString([]byte{9, 8}) {
		t.Fatalf("inlineData = %v", img)
	}
}

func TestGeminiEmptyFunctionCallID(t *testing.T) {
	// A functionCall with no id keeps ID "" on the block.
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":"pi"}}}]},"finishReason":"STOP"}]}`),
	}}
	msg, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("look up pi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if msg.Blocks[0].ID != "" || msg.Blocks[0].Name != "lookup" {
		t.Fatalf("tool block = %+v, want empty ID", msg.Blocks[0])
	}

	// Replay normalization synthesizes call_<n> for the empty ID and pairs
	// the result through the same mapping.
	history := []Message{
		UserText("look up pi"),
		msg,
		ToolResultMessage("", "lookup", false, TextBlock("3.14159")),
	}
	norm := normalizeMessages(gemModel(), history)
	if got := norm[1].Blocks[0].ID; got != "call_1" {
		t.Fatalf("normalized call ID = %q, want call_1", got)
	}
	if got := norm[2].ToolCallID; got != "call_1" {
		t.Fatalf("normalized result ID = %q, want call_1", got)
	}

	// Follow-up request replays the pair as functionCall + functionResponse.
	ct2 := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"pi is 3.14159"}]},"finishReason":"STOP"}]}`),
	}}
	if _, err := gemClient(ct2).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: history,
	}); err != nil {
		t.Fatalf("follow-up Complete: %v", err)
	}
	body := ct2.lastBody(t)
	fc := gemPath(t, body, "contents", 1, "parts", 0, "functionCall").(map[string]any)
	if fc["name"] != "lookup" {
		t.Fatalf("replayed functionCall = %v", fc)
	}
	fr := gemPath(t, body, "contents", 2, "parts", 0, "functionResponse").(map[string]any)
	if fr["name"] != "lookup" {
		t.Fatalf("replayed functionResponse = %v", fr)
	}
	if got := gemPath(t, fr, "response", "output"); got != "3.14159" {
		t.Fatalf("replayed output = %v", got)
	}
}

func TestGeminiError400(t *testing.T) {
	ct := &captureTransport{status: 400, chunks: []string{
		`{"error":{"code":400,"message":"invalid argument: contents"}}`,
	}}
	s := gemClient(ct).Stream(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("hi")},
	})
	events := collectEvents(s)
	want := []EventType{EventStart, EventError}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	msg, err := s.Message()
	if err == nil {
		t.Fatal("Message: want error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("err = %v, want *HTTPError 400", err)
	}
	if msg.StopReason != StopError {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	if !strings.Contains(msg.ErrorText, "invalid argument: contents") {
		t.Fatalf("ErrorText = %q, want provider body verbatim", msg.ErrorText)
	}
}

func TestGeminiSafetyFinish(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`),
		gemData(`{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`),
	}}
	s := gemClient(ct).Stream(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("hi")},
	})
	events := collectEvents(s)
	if events[len(events)-1].Type != EventError {
		t.Fatalf("last event = %v, want error", events[len(events)-1].Type)
	}
	msg, err := s.Message()
	if err == nil || !strings.Contains(err.Error(), "gemini: finish reason SAFETY") {
		t.Fatalf("err = %v", err)
	}
	if msg.StopReason != StopError {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	if textOf(msg) != "partial" {
		t.Fatalf("partial content lost: %q", textOf(msg))
	}
}

func TestGeminiPromptBlocked(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"promptFeedback":{"blockReason":"SAFETY"}}`),
	}}
	msg, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "gemini: prompt blocked: SAFETY") {
		t.Fatalf("err = %v", err)
	}
	if msg.StopReason != StopError {
		t.Fatalf("stop = %q", msg.StopReason)
	}
}

func TestGeminiAbortMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"Hello "}]}}]}`),
		gemData(`{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}]}`),
	}}
	s := gemClient(ct).Stream(ctx, Request{
		Model:    gemModel(),
		Messages: []Message{UserText("hi")},
	})
	var events []Event
	for ev := range s.Events() {
		events = append(events, ev)
		if ev.Type == EventTextDelta {
			cancel() // the transport stops serving chunks on the next read
		}
	}
	last := events[len(events)-1]
	if last.Type != EventError || !errors.Is(last.Err, context.Canceled) {
		t.Fatalf("last event = %v (err %v), want error(context.Canceled)", last.Type, last.Err)
	}
	msg, err := s.Message()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if msg.StopReason != StopAborted {
		t.Fatalf("stop = %q, want aborted", msg.StopReason)
	}
	if textOf(msg) != "Hello " {
		t.Fatalf("partial content = %q, want %q", textOf(msg), "Hello ")
	}
}

func TestGeminiUnicodeSplit(t *testing.T) {
	full := gemData(`{"candidates":[{"content":{"parts":[{"text":"héllo 🌍 wörld 🎉"}]},"finishReason":"STOP"}]}`)
	// Split the raw SSE bytes in the middle of the emoji's UTF-8 sequence.
	cut := strings.Index(full, "🌍") + 2
	ct := &captureTransport{chunks: []string{full[:cut], full[cut : cut+5], full[cut+5:]}}
	msg, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if textOf(msg) != "héllo 🌍 wörld 🎉" {
		t.Fatalf("text = %q", textOf(msg))
	}
}

func TestGeminiCassetteRoundTrip(t *testing.T) {
	// Record against a scripted transport into a temp dir, then replay from
	// the same cassette; no hand-authored cassette files are committed.
	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatal(err)
		}
	}()

	req := Request{
		Model:    gemModel(),
		System:   "be brief",
		Messages: []Message{UserText("hi")},
	}

	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"Hello "}]}}]}`),
		gemData(`{"candidates":[{"content":{"parts":[{"text":"world"}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`),
	}}
	rec := cassette.Record(filepath.Join("testdata", "cassettes", "gemini_roundtrip.json"), ct)
	recClient := New(WithTransport(rec), WithAPIKey("google", "gem-secret-key"))
	recorded, err := recClient.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("record Complete: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("cassette close: %v", err)
	}

	// The key must never reach disk (R23).
	raw, err := os.ReadFile(filepath.Join(tmp, "testdata", "cassettes", "gemini_roundtrip.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "gem-secret-key") {
		t.Fatal("API key leaked into cassette file")
	}

	replayClient := New(WithTransport(cassette.Replay(t, "gemini_roundtrip")), WithAPIKey("google", "gem-secret-key"))
	replayed, err := replayClient.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("replay Complete: %v", err)
	}
	if textOf(replayed) != textOf(recorded) {
		t.Fatalf("replayed text = %q, recorded %q", textOf(replayed), textOf(recorded))
	}
	if replayed.StopReason != recorded.StopReason {
		t.Fatalf("stop = %q vs %q", replayed.StopReason, recorded.StopReason)
	}
	if !reflect.DeepEqual(replayed.Usage, recorded.Usage) {
		t.Fatalf("usage = %+v vs %+v", replayed.Usage, recorded.Usage)
	}
}

// TestGeminiV3ThinkingEncoding pins the Gemini 3.x request shape (R14): the
// effort level goes over categorically inside thinkingConfig, the mutually
// exclusive budget field is omitted, and the sampling parameters that
// generation deprecated are dropped rather than sent and ignored.
func TestGeminiV3ThinkingEncoding(t *testing.T) {
	model := gemModel()
	model.Quirks.GeminiV3 = true

	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`),
	}}
	temp := 0.0
	if _, err := gemClient(ct).Complete(context.Background(), Request{
		Model:       model,
		Messages:    []Message{UserText("think")},
		Reasoning:   EffortMedium,
		Temperature: &temp,
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	cfg := gemPath(t, ct.lastBody(t), "generationConfig").(map[string]any)
	tc, _ := cfg["thinkingConfig"].(map[string]any)
	if tc == nil {
		t.Fatalf("no thinkingConfig: %v", cfg)
	}
	if tc["thinkingLevel"] != "medium" {
		t.Errorf("thinkingLevel = %v, want medium", tc["thinkingLevel"])
	}
	if tc["includeThoughts"] != true {
		t.Errorf("includeThoughts = %v, want true", tc["includeThoughts"])
	}
	// Sending both reasoning controls is itself a 400.
	if _, has := tc["thinkingBudget"]; has {
		t.Error("thinkingBudget sent alongside thinkingLevel")
	}
	if _, has := cfg["temperature"]; has {
		t.Error("temperature must be omitted on gemini 3.x (deprecated)")
	}

	// The 2.5 encoding must be untouched by the new branch.
	ct2 := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`),
	}}
	if _, err := gemClient(ct2).Complete(context.Background(), Request{
		Model:       gemModel(),
		Messages:    []Message{UserText("think")},
		Reasoning:   EffortMedium,
		Temperature: &temp,
	}); err != nil {
		t.Fatalf("Complete (2.5): %v", err)
	}
	cfg2 := gemPath(t, ct2.lastBody(t), "generationConfig").(map[string]any)
	tc2, _ := cfg2["thinkingConfig"].(map[string]any)
	if tc2 == nil || tc2["thinkingBudget"] != float64(8192) {
		t.Errorf("2.5 thinkingConfig = %v, want budget 8192", cfg2["thinkingConfig"])
	}
	if _, has := tc2["thinkingLevel"]; has {
		t.Error("3.x thinkingLevel leaked onto a 2.5 model")
	}
	if cfg2["temperature"] != float64(0) {
		t.Errorf("2.5 temperature = %v, want it still sent", cfg2["temperature"])
	}
}

// TestGeminiToolCallThoughtSignature covers the 3.x rule that a functionCall
// carries a thought signature which must be handed back on the next turn:
// omit it and the API rejects the whole request.
func TestGeminiToolCallThoughtSignature(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[` +
			`{"functionCall":{"name":"lookup","args":{"q":"pi"}},"thoughtSignature":"sig-fc-1"}]},` +
			`"finishReason":"STOP"}]}`),
	}}
	msg, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("look up pi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := msg.Blocks[0].Signature; got != "sig-fc-1" {
		t.Fatalf("tool call signature = %q, want it captured off the functionCall part", got)
	}

	// Replaying that history must put the signature back on the call.
	ct2 := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"3.14159"}]},"finishReason":"STOP"}]}`),
	}}
	if _, err := gemClient(ct2).Complete(context.Background(), Request{
		Model: gemModel(),
		Messages: []Message{
			UserText("look up pi"), msg,
			ToolResultMessage("", "lookup", false, TextBlock("3.14159")),
		},
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	part := gemPath(t, ct2.lastBody(t), "contents", 1, "parts", 0).(map[string]any)
	if part["thoughtSignature"] != "sig-fc-1" {
		t.Errorf("replayed functionCall part = %v, want thoughtSignature sig-fc-1", part)
	}
}

// TestGeminiEmptyThoughtDropped covers the Part contract: exactly one data
// member. A thinking block with no text has none, and sending it fails the
// entire request — which is the default shape on 3.x, where thinking runs
// but summaries are only returned when asked for.
func TestGeminiEmptyThoughtDropped(t *testing.T) {
	assistant := Message{
		Role: RoleAssistant, Model: "gemini-test", Provider: "google", API: GoogleGemini,
		Blocks: []Block{
			{Type: BlockThinking, Text: "", Signature: "sig-empty"},
			{Type: BlockText, Text: "the answer"},
		},
	}
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`),
	}}
	if _, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserText("q"), assistant, UserText("next")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	parts := gemPath(t, ct.lastBody(t), "contents", 1, "parts").([]any)
	for _, raw := range parts {
		p := raw.(map[string]any)
		if _, hasText := p["text"]; !hasText && p["inlineData"] == nil &&
			p["functionCall"] == nil && p["functionResponse"] == nil {
			t.Errorf("part with no data member would be rejected: %v", p)
		}
	}
	if len(parts) != 1 {
		t.Errorf("parts = %d, want 1 (the empty thought is dropped)", len(parts))
	}
}

func TestGeminiDocumentInput(t *testing.T) {
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"a spec"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model:    gemModel(),
		Messages: []Message{UserBlocks(TextBlock("read"), DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3}))},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	doc := gemPath(t, body, "contents", 0, "parts", 1, "inlineData").(map[string]any)
	if doc["mimeType"] != "application/pdf" {
		t.Fatalf("mimeType = %v", doc["mimeType"])
	}
	if doc["data"] != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("data = %v", doc["data"])
	}
}

// TestGeminiDocumentInToolResultDropped (R38): a functionResponse part
// carries text, and images ride alongside as extra inlineData parts. A
// document is dropped rather than smuggled in as an untyped blob.
func TestGeminiDocumentInToolResultDropped(t *testing.T) {
	assistant := Message{
		Role: RoleAssistant, Model: "gem-test-model",
		Blocks: []Block{{Type: BlockToolCall, ID: "call_1", Name: "fetch", Args: json.RawMessage(`{}`)}},
	}
	ct := &captureTransport{chunks: []string{
		gemData(`{"candidates":[{"content":{"parts":[{"text":"nice"}]},"finishReason":"STOP"}]}`),
	}}
	_, err := gemClient(ct).Complete(context.Background(), Request{
		Model: gemModel(),
		Messages: []Message{
			UserText("fetch the spec"),
			assistant,
			ToolResultMessage("call_1", "fetch", false,
				TextBlock("got it"), DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3})),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := ct.lastBody(t)
	parts := gemPath(t, body, "contents", 2, "parts").([]any)
	if len(parts) != 1 {
		t.Fatalf("tool result parts = %d, want 1 (functionResponse only): %v", len(parts), parts)
	}
	fr := gemPath(t, body, "contents", 2, "parts", 0, "functionResponse").(map[string]any)
	if got := gemPath(t, fr, "response", "output"); got != "got it" {
		t.Fatalf("output = %v", got)
	}
}

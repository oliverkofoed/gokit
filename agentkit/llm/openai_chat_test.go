package llm

// Tests for the openai-chat protocol (SPEC §8.2, §12.2). Helpers here are
// prefixed oaic; generic helpers live in wiretest_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/cassette"
	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// oaicModel builds a test model on the openai-chat protocol. The provider
// name is unlikely to have an ambient env key (OAICTEST_API_KEY).
func oaicModel(q Quirks) Model {
	return Model{
		ID:            "oaic-test-model",
		API:           OpenAIChat,
		Provider:      "oaictest",
		BaseURL:       "https://api.test/v1",
		Cost:          Cost{Input: 1, Output: 2, CacheRead: 0.5},
		ContextWindow: 128_000,
		MaxOutput:     4096,
		Reasoning:     true,
		Vision:        true,
		Headers:       map[string]string{"X-Extra": "1"},
		Quirks:        q,
	}
}

func oaicClient(tr transport.Interface) *Client {
	return New(WithTransport(tr), WithAPIKey("oaictest", "test-key"))
}

// oaicDoneChunks is a minimal successful text stream.
func oaicDoneChunks() []string {
	return []string{
		sseChunk("", `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`),
		"data: [DONE]\n\n",
	}
}

// oaicWireMessages extracts the encoded messages array from a captured body.
func oaicWireMessages(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages missing or not an array: %v", body["messages"])
	}
	out := make([]map[string]any, len(raw))
	for i, m := range raw {
		out[i], ok = m.(map[string]any)
		if !ok {
			t.Fatalf("messages[%d] not an object: %v", i, m)
		}
	}
	return out
}

// oaicRunPayload completes a request against a scripted success and returns
// the captured request body.
func oaicRunPayload(t *testing.T, req Request) map[string]any {
	t.Helper()
	tr := &captureTransport{chunks: oaicDoneChunks()}
	if _, err := oaicClient(tr).Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return tr.lastBody(t)
}

func TestOpenAIChatBasicText(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		sseChunk("", `{"choices":[{"delta":{"role":"assistant","content":""}}]}`),
		sseChunk("", `{"choices":[{"delta":{"content":"Hello "}}]}`),
		sseChunk("", `{"choices":[{"delta":{"content":"world"}}]}`),
		sseChunk("", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`),
		sseChunk("", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}`),
		"data: [DONE]\n\n",
	}}
	temp := 0.5
	s := oaicClient(tr).Stream(context.Background(), Request{
		Model:       oaicModel(Quirks{}),
		System:      "Be helpful.",
		Messages:    []Message{UserText("Hi")},
		Reasoning:   EffortHigh,
		Temperature: &temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// Payload golden.
	treq := tr.lastReq(t)
	if treq.Method != http.MethodPost || treq.URL != "https://api.test/v1/chat/completions" {
		t.Fatalf("request = %s %s", treq.Method, treq.URL)
	}
	if got := treq.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := treq.Header.Get("X-Extra"); got != "1" {
		t.Fatalf("model header X-Extra = %q", got)
	}
	body := tr.lastBody(t)
	if body["model"] != "oaic-test-model" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %v", body["stream"])
	}
	so, ok := body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options = %v", body["stream_options"])
	}
	if body["max_completion_tokens"] != float64(4096) {
		t.Fatalf("max_completion_tokens = %v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatal("unexpected max_tokens field")
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", body["reasoning_effort"])
	}
	if body["temperature"] != 0.5 {
		t.Fatalf("temperature = %v", body["temperature"])
	}
	msgs := oaicWireMessages(t, body)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "Be helpful." {
		t.Fatalf("system message = %v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "Hi" {
		t.Fatalf("user message = %v (content must be a plain string)", msgs[1])
	}

	// Streamed result.
	wantTypes := []EventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventDone}
	if !reflect.DeepEqual(eventTypes(events), wantTypes) {
		t.Fatalf("events = %v, want %v", eventTypes(events), wantTypes)
	}
	if got := textOf(msg); got != "Hello world" {
		t.Fatalf("text = %q", got)
	}
	if msg.StopReason != StopEnd {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	u := msg.Usage
	if u == nil || u.Input != 10 || u.Output != 5 || u.CacheRead != 4 {
		t.Fatalf("usage = %+v", u)
	}
	wantCost := (10*1.0 + 5*2.0 + 4*0.5) / 1e6
	if u.TotalCost != wantCost {
		t.Fatalf("cost = %v, want %v", u.TotalCost, wantCost)
	}
}

func TestOpenAIChatToolCall(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Pa"}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ris\"}"}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`),
		sseChunk("", `{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8}}`),
		"data: [DONE]\n\n",
	}}
	s := oaicClient(tr).Stream(context.Background(), Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("Weather in Paris?")},
		Tools: []ToolDef{{
			Name:        "get_weather",
			Description: "Get weather for a city",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
	})

	var partials []string
	for ev := range s.Events() {
		if ev.Type == EventToolCallDelta {
			partials = append(partials, string(ev.Message.Blocks[ev.Index].Args))
		}
	}
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// Tool def encoding.
	body := tr.lastBody(t)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	wrapper := tools[0].(map[string]any)
	if wrapper["type"] != "function" {
		t.Fatalf("tool type = %v", wrapper["type"])
	}
	fn := wrapper["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["description"] != "Get weather for a city" {
		t.Fatalf("function = %v", fn)
	}
	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Fatalf("parameters = %v", params)
	}

	// Streamed argument fragments: partial parse, then the complete Args.
	want := []string{`{"city":"Pa"}`, `{"city":"Paris"}`}
	if !reflect.DeepEqual(partials, want) {
		t.Fatalf("partial args = %q, want %q", partials, want)
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	if len(msg.Blocks) != 1 {
		t.Fatalf("blocks = %+v", msg.Blocks)
	}
	b := msg.Blocks[0]
	if b.Type != BlockToolCall || b.ID != "call_abc" || b.Name != "get_weather" || string(b.Args) != `{"city":"Paris"}` {
		t.Fatalf("tool block = %+v", b)
	}
	if msg.Usage == nil || msg.Usage.Input != 20 || msg.Usage.Output != 8 {
		t.Fatalf("usage = %+v", msg.Usage)
	}
}

func TestOpenAIChatToolResultRoundtrip(t *testing.T) {
	assistant := Message{
		Role:  RoleAssistant,
		Model: "oaic-test-model", // same model: thinking survives normalization, protocol must drop it
		Blocks: []Block{
			{Type: BlockThinking, Text: "secret reasoning", Signature: "sig"},
			TextBlock("Checking the weather."),
			{Type: BlockToolCall, ID: "call_1", Name: "get_weather", Args: json.RawMessage(`{"city":"Paris"}`)},
		},
	}
	body := oaicRunPayload(t, Request{
		Model: oaicModel(Quirks{}),
		Messages: []Message{
			UserText("Weather?"),
			assistant,
			ToolResultMessage("call_1", "get_weather", false, TextBlock("Sunny, 22C")),
		},
	})
	msgs := oaicWireMessages(t, body)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	am := msgs[1]
	if am["role"] != "assistant" || am["content"] != "Checking the weather." {
		t.Fatalf("assistant message = %v", am)
	}
	calls, ok := am["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %v", am["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Fatalf("tool call = %v", call)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatalf("function = %v", fn)
	}
	args, ok := fn["arguments"].(string) // arguments must be a JSON *string*
	if !ok {
		t.Fatalf("arguments not a string: %T %v", fn["arguments"], fn["arguments"])
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(args), &decoded); err != nil || decoded["city"] != "Paris" {
		t.Fatalf("arguments = %q (err %v)", args, err)
	}
	if raw, _ := json.Marshal(am); strings.Contains(string(raw), "secret reasoning") {
		t.Fatalf("thinking leaked into wire message: %s", raw)
	}
	tm := msgs[2]
	if tm["role"] != "tool" || tm["tool_call_id"] != "call_1" || tm["content"] != "Sunny, 22C" {
		t.Fatalf("tool message = %v", tm)
	}
}

func TestOpenAIChatImageInput(t *testing.T) {
	body := oaicRunPayload(t, Request{
		Model: oaicModel(Quirks{}),
		Messages: []Message{
			UserBlocks(TextBlock("Look:"), ImageBlock("image/png", []byte{1, 2, 3})),
		},
	})
	msgs := oaicWireMessages(t, body)
	parts, ok := msgs[0]["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %v (want a 2-part array)", msgs[0]["content"])
	}
	tp := parts[0].(map[string]any)
	if tp["type"] != "text" || tp["text"] != "Look:" {
		t.Fatalf("text part = %v", tp)
	}
	ip := parts[1].(map[string]any)
	if ip["type"] != "image_url" {
		t.Fatalf("image part = %v", ip)
	}
	iu := ip["image_url"].(map[string]any)
	if iu["url"] != "data:image/png;base64,AQID" {
		t.Fatalf("image url = %v", iu["url"])
	}
}

func TestOpenAIChatImageInToolResult(t *testing.T) {
	body := oaicRunPayload(t, Request{
		Model: oaicModel(Quirks{}),
		Messages: []Message{
			UserText("Take a screenshot"),
			{
				Role:   RoleAssistant,
				Model:  "oaic-test-model",
				Blocks: []Block{{Type: BlockToolCall, ID: "call_1", Name: "screenshot", Args: json.RawMessage(`{}`)}},
			},
			ToolResultMessage("call_1", "screenshot", false,
				TextBlock("screenshot taken"), ImageBlock("image/png", []byte{1, 2, 3})),
		},
	})
	msgs := oaicWireMessages(t, body)
	if len(msgs) != 4 {
		t.Fatalf("want 4 wire messages (user, assistant, tool, spillover user), got %v", msgs)
	}
	tm := msgs[2]
	if tm["role"] != "tool" || tm["tool_call_id"] != "call_1" || tm["content"] != "screenshot taken" {
		t.Fatalf("tool message = %v", tm)
	}
	spill := msgs[3]
	if spill["role"] != "user" {
		t.Fatalf("spillover role = %v", spill["role"])
	}
	parts, ok := spill["content"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("spillover content = %v", spill["content"])
	}
	ip := parts[0].(map[string]any)
	if ip["type"] != "image_url" || ip["image_url"].(map[string]any)["url"] != "data:image/png;base64,AQID" {
		t.Fatalf("spillover image part = %v", ip)
	}
}

func TestOpenAIChatMultiToolParallel(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		sseChunk("", `{"choices":[{"delta":{"content":"Checking"}}]}`),
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","function":{"name":"a","arguments":"{\"x\":"}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c1","function":{"name":"b","arguments":"{\"y\":"}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}},{"index":1,"function":{"arguments":"2}"}}]}}]}`),
		sseChunk("", `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`),
		"data: [DONE]\n\n",
	}}
	s := oaicClient(tr).Stream(context.Background(), Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("go")},
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	type te struct {
		typ EventType
		idx int
	}
	var got []te
	for _, ev := range events {
		got = append(got, te{ev.Type, ev.Index})
	}
	want := []te{
		{EventStart, 0},
		{EventTextStart, 0}, {EventTextDelta, 0},
		{EventToolCallStart, 1}, {EventToolCallDelta, 1},
		{EventToolCallStart, 2}, {EventToolCallDelta, 2},
		{EventToolCallDelta, 1}, {EventToolCallDelta, 2},
		{EventTextEnd, 0}, {EventToolCallEnd, 1}, {EventToolCallEnd, 2},
		{EventDone, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}

	if len(msg.Blocks) != 3 {
		t.Fatalf("blocks = %+v", msg.Blocks)
	}
	if msg.Blocks[0].Type != BlockText || msg.Blocks[0].Text != "Checking" {
		t.Fatalf("block 0 = %+v", msg.Blocks[0])
	}
	if b := msg.Blocks[1]; b.Type != BlockToolCall || b.ID != "c0" || b.Name != "a" || string(b.Args) != `{"x":1}` {
		t.Fatalf("block 1 = %+v", b)
	}
	if b := msg.Blocks[2]; b.Type != BlockToolCall || b.ID != "c1" || b.Name != "b" || string(b.Args) != `{"y":2}` {
		t.Fatalf("block 2 = %+v", b)
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q", msg.StopReason)
	}
}

func TestOpenAIChatQuirks(t *testing.T) {
	base := func(q Quirks) Request {
		return Request{
			Model:     oaicModel(q),
			System:    "Be helpful.",
			Messages:  []Message{UserText("Hi")},
			Reasoning: EffortMedium,
		}
	}

	t.Run("max_tokens_field", func(t *testing.T) {
		body := oaicRunPayload(t, base(Quirks{MaxTokensField: "max_tokens"}))
		if body["max_tokens"] != float64(4096) {
			t.Fatalf("max_tokens = %v", body["max_tokens"])
		}
		if _, ok := body["max_completion_tokens"]; ok {
			t.Fatal("max_completion_tokens must be absent when MaxTokensField overrides it")
		}
	})

	t.Run("no_stream_usage", func(t *testing.T) {
		body := oaicRunPayload(t, base(Quirks{NoStreamUsage: true}))
		if _, ok := body["stream_options"]; ok {
			t.Fatalf("stream_options must be absent: %v", body["stream_options"])
		}
	})

	t.Run("no_reasoning_effort", func(t *testing.T) {
		body := oaicRunPayload(t, base(Quirks{NoReasoningEffort: true}))
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("reasoning_effort must be absent: %v", body["reasoning_effort"])
		}
	})

	t.Run("non_reasoning_model", func(t *testing.T) {
		req := base(Quirks{})
		req.Model.Reasoning = false
		body := oaicRunPayload(t, req)
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("reasoning_effort must be absent on non-reasoning models: %v", body["reasoning_effort"])
		}
	})

	t.Run("anthropic_cache_control", func(t *testing.T) {
		req := base(Quirks{AnthropicCacheControl: true})
		req.Messages = []Message{
			UserText("first"),
			{Role: RoleAssistant, Model: "oaic-test-model", Blocks: []Block{TextBlock("reply")}},
			UserText("last"),
		}
		body := oaicRunPayload(t, req)
		msgs := oaicWireMessages(t, body)

		sys, ok := msgs[0]["content"].([]any)
		if !ok || len(sys) != 1 {
			t.Fatalf("system content = %v (want cache_control part array)", msgs[0]["content"])
		}
		sp := sys[0].(map[string]any)
		if sp["type"] != "text" || sp["text"] != "Be helpful." {
			t.Fatalf("system part = %v", sp)
		}
		if cc, ok := sp["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
			t.Fatalf("system cache_control = %v", sp["cache_control"])
		}

		if msgs[1]["content"] != "first" {
			t.Fatalf("middle user message must stay a plain string: %v", msgs[1]["content"])
		}

		last, ok := msgs[3]["content"].([]any)
		if !ok || len(last) != 1 {
			t.Fatalf("last content = %v", msgs[3]["content"])
		}
		lp := last[0].(map[string]any)
		if lp["text"] != "last" {
			t.Fatalf("last part = %v", lp)
		}
		if cc, ok := lp["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
			t.Fatalf("last cache_control = %v", lp["cache_control"])
		}
	})

	t.Run("cache_control_disabled", func(t *testing.T) {
		req := base(Quirks{AnthropicCacheControl: true})
		req.DisableCache = true
		body := oaicRunPayload(t, req)
		msgs := oaicWireMessages(t, body)
		if msgs[0]["content"] != "Be helpful." || msgs[1]["content"] != "Hi" {
			t.Fatalf("DisableCache must keep plain string content: %v", msgs)
		}
		if raw, _ := json.Marshal(body); strings.Contains(string(raw), "cache_control") {
			t.Fatalf("cache_control leaked with DisableCache: %s", raw)
		}
	})
}

func TestOpenAIChatNoAuthHeader(t *testing.T) {
	tr := &captureTransport{chunks: oaicDoneChunks()}
	client := New(WithTransport(tr), WithAPIKey("oaictest", NoAuth))
	if _, err := client.Complete(context.Background(), Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("Hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := tr.lastReq(t).Header["Authorization"]; ok {
		t.Fatal("Authorization header must be absent with NoAuth")
	}
}

func TestOpenAIChatError400(t *testing.T) {
	tr := &captureTransport{
		status: 400,
		header: http.Header{"Content-Type": []string{"application/json"}},
		chunks: []string{`{"error":{"message":"bad request: unknown model"}}`},
	}
	msg, err := oaicClient(tr).Complete(context.Background(), Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("Hi")},
	})
	if err == nil {
		t.Fatal("want error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("err = %v, want *HTTPError with status 400", err)
	}
	if msg.StopReason != StopError {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	if !strings.Contains(msg.ErrorText, "bad request: unknown model") {
		t.Fatalf("ErrorText must carry the response body verbatim: %q", msg.ErrorText)
	}
}

func TestOpenAIChatAbortMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &captureTransport{chunks: []string{
		sseChunk("", `{"choices":[{"delta":{"content":"Hello "}}]}`),
		sseChunk("", `{"choices":[{"delta":{"content":"world"}}]}`),
		sseChunk("", `{"choices":[{"delta":{"content":" never seen"}}]}`),
		"data: [DONE]\n\n",
	}}
	s := oaicClient(tr).Stream(ctx, Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("Hi")},
	})
	for ev := range s.Events() {
		if ev.Type == EventTextDelta && ev.Delta == "world" {
			cancel() // the transport honors ctx between chunks
		}
	}
	msg, err := s.Message()
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if msg.StopReason != StopAborted {
		t.Fatalf("stop = %q", msg.StopReason)
	}
	if got := textOf(msg); got != "Hello world" {
		t.Fatalf("partial text = %q", got)
	}
	if msg.ErrorText == "" {
		t.Fatal("ErrorText must be set on abort")
	}
}

func TestOpenAIChatUnicodeSplitAcrossChunks(t *testing.T) {
	// One SSE event whose bytes are split across two transport chunks in the
	// middle of a multi-byte rune.
	full := sseChunk("", `{"choices":[{"delta":{"content":"Hi 🎉 done"}}]}`)
	cut := strings.Index(full, "🎉") + 2 // inside the emoji's UTF-8 bytes
	tr := &captureTransport{chunks: []string{
		full[:cut],
		full[cut:],
		sseChunk("", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`),
		"data: [DONE]\n\n",
	}}
	msg, err := oaicClient(tr).Complete(context.Background(), Request{
		Model:    oaicModel(Quirks{}),
		Messages: []Message{UserText("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := textOf(msg); got != "Hi 🎉 done" {
		t.Fatalf("text = %q", got)
	}
	if msg.StopReason != StopEnd {
		t.Fatalf("stop = %q", msg.StopReason)
	}
}

func TestOpenAIChatCassetteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oaic_roundtrip.json")

	scripted := &captureTransport{chunks: []string{
		sseChunk("", `{"choices":[{"delta":{"content":"Hello "}}]}`),
		sseChunk("", `{"choices":[{"delta":{"content":"cassette"}}]}`),
		sseChunk("", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`),
		sseChunk("", `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`),
		"data: [DONE]\n\n",
	}}
	req := Request{
		Model:    oaicModel(Quirks{}),
		System:   "Be brief.",
		Messages: []Message{UserText("Hi")},
	}

	// Record through the scripted transport into a temp cassette.
	rec := cassette.Record(path, scripted)
	recorded, err := New(WithTransport(rec), WithAPIKey("oaictest", "secret-key-abc")).
		Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("recorded Complete: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("cassette close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	if strings.Contains(string(raw), "secret-key-abc") {
		t.Fatal("API key leaked into cassette (R23)")
	}

	// Replay resolves names under testdata/cassettes relative to the package
	// dir; point it at the temp file via a relative name so nothing is
	// written into the real tree.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(cwd, "testdata", "cassettes"), path)
	if err != nil {
		t.Fatalf("rel path: %v", err)
	}
	replayTr := cassette.Replay(t, strings.TrimSuffix(rel, ".json"))
	replayed, err := New(WithTransport(replayTr), WithAPIKey("oaictest", "different-key")).
		Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed Complete: %v", err)
	}

	if textOf(replayed) != textOf(recorded) || textOf(replayed) != "Hello cassette" {
		t.Fatalf("replayed text = %q, recorded = %q", textOf(replayed), textOf(recorded))
	}
	if replayed.StopReason != recorded.StopReason || replayed.StopReason != StopEnd {
		t.Fatalf("stop: replayed %q, recorded %q", replayed.StopReason, recorded.StopReason)
	}
	if replayed.Usage == nil || recorded.Usage == nil ||
		replayed.Usage.Input != recorded.Usage.Input || replayed.Usage.Input != 7 ||
		replayed.Usage.Output != recorded.Usage.Output || replayed.Usage.Output != 3 {
		t.Fatalf("usage: replayed %+v, recorded %+v", replayed.Usage, recorded.Usage)
	}
}

// TestOpenAIChatReasoningEffortNone covers the ReasoningEffortNone quirk
// (R14). EffortOff means "no reasoning requested", but on a model that
// reasons by default, omitting reasoning_effort leaves reasoning ON — and
// gpt-5.6-luna then rejects any request carrying function tools:
//
//	Function tools with reasoning_effort are not supported for gpt-5.6-luna
//	in /v1/chat/completions. To use function tools, use /v1/responses or set
//	reasoning_effort to 'none'.
//
// The quirk is opt-in because older and OpenAI-compatible endpoints reject
// the "none" value outright.
func TestOpenAIChatReasoningEffortNone(t *testing.T) {
	reasoner := func(quirk bool) Model {
		m := Model{
			ID: "reasoning-model", API: OpenAIChat, Provider: "openai",
			BaseURL: "https://oai.example.com", Reasoning: true,
		}
		m.Quirks.ReasoningEffortNone = quirk
		return m
	}
	chunks := []string{
		sseChunk("", `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`),
		sseChunk("", "[DONE]"),
	}

	t.Run("effort off sends none", func(t *testing.T) {
		tr := &captureTransport{chunks: chunks}
		c := New(WithTransport(tr), WithAPIKey("openai", "k"))
		if _, err := c.Complete(context.Background(), Request{
			Model: reasoner(true), Messages: []Message{UserText("hi")},
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got := tr.lastBody(t)["reasoning_effort"]; got != "none" {
			t.Errorf("reasoning_effort = %v, want none", got)
		}
	})

	t.Run("explicit effort still wins", func(t *testing.T) {
		tr := &captureTransport{chunks: chunks}
		c := New(WithTransport(tr), WithAPIKey("openai", "k"))
		if _, err := c.Complete(context.Background(), Request{
			Model: reasoner(true), Messages: []Message{UserText("hi")}, Reasoning: EffortHigh,
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got := tr.lastBody(t)["reasoning_effort"]; got != "high" {
			t.Errorf("reasoning_effort = %v, want high", got)
		}
	})

	t.Run("without the quirk the field is omitted", func(t *testing.T) {
		tr := &captureTransport{chunks: chunks}
		c := New(WithTransport(tr), WithAPIKey("openai", "k"))
		if _, err := c.Complete(context.Background(), Request{
			Model: reasoner(false), Messages: []Message{UserText("hi")},
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, has := tr.lastBody(t)["reasoning_effort"]; has {
			t.Error("reasoning_effort sent to an endpoint that may reject the value")
		}
	})

	t.Run("non-reasoning model never gets the field", func(t *testing.T) {
		m := reasoner(true)
		m.Reasoning = false
		tr := &captureTransport{chunks: chunks}
		c := New(WithTransport(tr), WithAPIKey("openai", "k"))
		if _, err := c.Complete(context.Background(), Request{
			Model: m, Messages: []Message{UserText("hi")}, Reasoning: EffortHigh,
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, has := tr.lastBody(t)["reasoning_effort"]; has {
			t.Error("reasoning_effort sent to a non-reasoning model")
		}
	})
}

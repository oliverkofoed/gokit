package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/cassette"
	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// ---- helpers ----------------------------------------------------------------

func anthClient(tr transport.Interface, opts ...Option) *Client {
	return New(append([]Option{WithTransport(tr), WithAPIKey("anthropic", "test-key")}, opts...)...)
}

// anthGet navigates a decoded JSON value by string (map key) and int (slice
// index) path elements.
func anthGet(tb testing.TB, v any, path ...any) any {
	tb.Helper()
	for i, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				tb.Fatalf("path %v: element %d: want object, got %T", path, i, v)
			}
			v, ok = m[k]
			if !ok {
				tb.Fatalf("path %v: missing key %q in %v", path, k, m)
			}
		case int:
			s, ok := v.([]any)
			if !ok {
				tb.Fatalf("path %v: element %d: want array, got %T", path, i, v)
			}
			if k >= len(s) {
				tb.Fatalf("path %v: index %d out of range (len %d)", path, k, len(s))
			}
			v = s[k]
		default:
			tb.Fatalf("path %v: bad element %T", path, p)
		}
	}
	return v
}

func anthStart(usageJSON string) string {
	return sseChunk("message_start",
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","usage":`+usageJSON+`}}`)
}

func anthBlockStart(index int, blockJSON string) string {
	return sseChunk("content_block_start",
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, index, blockJSON))
}

func anthDelta(index int, deltaJSON string) string {
	return sseChunk("content_block_delta",
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":%s}`, index, deltaJSON))
}

func anthTextDelta(index int, text string) string {
	b, _ := json.Marshal(text)
	return anthDelta(index, `{"type":"text_delta","text":`+string(b)+`}`)
}

func anthInputJSONDelta(index int, fragment string) string {
	b, _ := json.Marshal(fragment)
	return anthDelta(index, `{"type":"input_json_delta","partial_json":`+string(b)+`}`)
}

func anthBlockStop(index int) string {
	return sseChunk("content_block_stop",
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}

func anthMsgDelta(stopReason string, outputTokens int) string {
	return sseChunk("message_delta",
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, stopReason, outputTokens))
}

func anthMsgStop() string {
	return sseChunk("message_stop", `{"type":"message_stop"}`)
}

// anthBasicChunks scripts a plain "Hello world" completion.
func anthBasicChunks() []string {
	return []string{
		anthStart(`{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":3,"cache_read_input_tokens":7}`),
		anthBlockStart(0, `{"type":"text","text":""}`),
		anthTextDelta(0, "Hello "),
		anthTextDelta(0, "world"),
		anthBlockStop(0),
		anthMsgDelta("end_turn", 5),
		anthMsgStop(),
	}
}

// ---- tests ------------------------------------------------------------------

func TestAnthropicBasicText(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)
	model := ClaudeSonnet45
	model.Headers = map[string]string{"x-extra": "yes"}
	temp := 0.7

	s := c.Stream(context.Background(), Request{
		Model:       model,
		System:      "be nice",
		Messages:    []Message{UserText("hi")},
		MaxTokens:   123,
		Temperature: &temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// Request line + headers.
	req := tr.lastReq(t)
	if req.Method != "POST" || req.URL != "https://api.anthropic.com/v1/messages" {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Errorf("x-api-key = %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got)
	}
	if got := req.Header.Get("x-extra"); got != "yes" {
		t.Errorf("model extra header = %q", got)
	}

	// Payload shape.
	body := tr.lastBody(t)
	if body["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v", body["model"])
	}
	if body["max_tokens"] != float64(123) {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v", body["temperature"])
	}
	if got := anthGet(t, body, "system", 0, "type"); got != "text" {
		t.Errorf("system[0].type = %v", got)
	}
	if got := anthGet(t, body, "system", 0, "text"); got != "be nice" {
		t.Errorf("system[0].text = %v", got)
	}
	if got := anthGet(t, body, "system", 0, "cache_control", "type"); got != "ephemeral" {
		t.Errorf("system cache_control = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "role"); got != "user" {
		t.Errorf("messages[0].role = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 0, "text"); got != "hi" {
		t.Errorf("messages[0].content[0].text = %v", got)
	}
	// Last block of the final message carries the third breakpoint (R15).
	if got := anthGet(t, body, "messages", 0, "content", 0, "cache_control", "type"); got != "ephemeral" {
		t.Errorf("final message cache_control = %v", got)
	}

	// Event order and content.
	want := []EventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventDone}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Errorf("event types = %v, want %v", got, want)
	}
	if got := textOf(msg); got != "Hello world" {
		t.Errorf("text = %q", got)
	}
	if msg.StopReason != StopEnd {
		t.Errorf("stop = %q", msg.StopReason)
	}
	u := msg.Usage
	if u == nil || u.Input != 10 || u.Output != 5 || u.CacheWrite != 3 || u.CacheRead != 7 {
		t.Errorf("usage = %+v", u)
	}
	if u != nil && u.TotalCost <= 0 {
		t.Errorf("total cost = %v", u.TotalCost)
	}
}

func TestAnthropicToolCall(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		anthStart(`{"input_tokens":20,"output_tokens":1}`),
		anthBlockStart(0, `{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}`),
		anthInputJSONDelta(0, `{"city":"Cope`),
		anthInputJSONDelta(0, `nhagen"}`),
		anthBlockStop(0),
		anthMsgDelta("tool_use", 9),
		anthMsgStop(),
	}}
	c := anthClient(tr)
	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)

	s := c.Stream(context.Background(), Request{
		Model:    ClaudeSonnet45,
		Messages: []Message{UserText("weather?")},
		Tools:    []ToolDef{{Name: "get_weather", Description: "Look up weather", Schema: schema}},
	})

	var deltaArgs []string
	for ev := range s.Events() {
		if ev.Type == EventToolCallDelta {
			args := ev.Message.Blocks[0].Args
			if !json.Valid(args) {
				t.Errorf("mid-stream Args not valid JSON: %s", args)
			}
			deltaArgs = append(deltaArgs, string(args))
		}
	}
	if len(deltaArgs) != 2 {
		t.Fatalf("tool_call_delta count = %d", len(deltaArgs))
	}

	// Tool def encoding: input_schema is a passthrough.
	body := tr.lastBody(t)
	if got := anthGet(t, body, "tools", 0, "name"); got != "get_weather" {
		t.Errorf("tools[0].name = %v", got)
	}
	if got := anthGet(t, body, "tools", 0, "description"); got != "Look up weather" {
		t.Errorf("tools[0].description = %v", got)
	}
	var wantSchema any
	if err := json.Unmarshal(schema, &wantSchema); err != nil {
		t.Fatal(err)
	}
	if got := anthGet(t, body, "tools", 0, "input_schema"); !reflect.DeepEqual(got, wantSchema) {
		t.Errorf("input_schema = %v, want %v", got, wantSchema)
	}
	// Last tool def carries a breakpoint (R15).
	if got := anthGet(t, body, "tools", 0, "cache_control", "type"); got != "ephemeral" {
		t.Errorf("tool cache_control = %v", got)
	}

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if msg.StopReason != StopToolUse {
		t.Errorf("stop = %q", msg.StopReason)
	}
	b := msg.Blocks[0]
	if b.Type != BlockToolCall || b.ID != "toolu_1" || b.Name != "get_weather" {
		t.Errorf("tool block = %+v", b)
	}
	var args map[string]string
	if err := json.Unmarshal(b.Args, &args); err != nil || args["city"] != "Copenhagen" {
		t.Errorf("args = %s (err %v)", b.Args, err)
	}
}

func TestAnthropicToolResultRoundtrip(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	history := []Message{
		UserText("weather in cph and aarhus?"),
		{Role: RoleAssistant, Model: "claude-sonnet-4-5", Blocks: []Block{
			TextBlock("checking"),
			{Type: BlockToolCall, ID: "call_1", Name: "get_weather", Args: json.RawMessage(`{"city":"cph"}`)},
			{Type: BlockToolCall, ID: "call_2", Name: "get_weather", Args: json.RawMessage(`{"city":"aarhus"}`)},
		}},
		ToolResultMessage("call_1", "get_weather", false, TextBlock("sunny")),
		ToolResultMessage("call_2", "get_weather", true, TextBlock("boom")),
	}
	if _, err := c.Complete(context.Background(), Request{Model: ClaudeSonnet45, Messages: history}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	msgs := anthGet(t, body, "messages").([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3 (consecutive tool results must merge)", len(msgs))
	}

	// Assistant replay: text + two tool_use blocks.
	if got := anthGet(t, body, "messages", 1, "role"); got != "assistant" {
		t.Errorf("messages[1].role = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 0, "type"); got != "text" {
		t.Errorf("messages[1].content[0].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 1, "type"); got != "tool_use" {
		t.Errorf("messages[1].content[1].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 1, "id"); got != "call_1" {
		t.Errorf("tool_use id = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 1, "input", "city"); got != "cph" {
		t.Errorf("tool_use input = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 2, "id"); got != "call_2" {
		t.Errorf("second tool_use id = %v", got)
	}

	// Both tool results merged into ONE user message.
	if got := anthGet(t, body, "messages", 2, "role"); got != "user" {
		t.Errorf("messages[2].role = %v", got)
	}
	content := anthGet(t, body, "messages", 2, "content").([]any)
	if len(content) != 2 {
		t.Fatalf("merged tool_result content len = %d, want 2", len(content))
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "type"); got != "tool_result" {
		t.Errorf("content[0].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "tool_use_id"); got != "call_1" {
		t.Errorf("content[0].tool_use_id = %v", got)
	}
	if _, has := content[0].(map[string]any)["is_error"]; has {
		t.Errorf("is_error present on non-error result: %v", content[0])
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "content", 0, "text"); got != "sunny" {
		t.Errorf("nested result text = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 1, "tool_use_id"); got != "call_2" {
		t.Errorf("content[1].tool_use_id = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 1, "is_error"); got != true {
		t.Errorf("content[1].is_error = %v", got)
	}
	// Final message's last block carries the breakpoint.
	if got := anthGet(t, body, "messages", 2, "content", 1, "cache_control", "type"); got != "ephemeral" {
		t.Errorf("final block cache_control = %v", got)
	}
}

func TestAnthropicThinking(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		anthStart(`{"input_tokens":15,"output_tokens":1}`),
		anthBlockStart(0, `{"type":"thinking","thinking":""}`),
		anthDelta(0, `{"type":"thinking_delta","thinking":"Let me "}`),
		anthDelta(0, `{"type":"thinking_delta","thinking":"think"}`),
		anthDelta(0, `{"type":"signature_delta","signature":"sig123"}`),
		anthBlockStop(0),
		anthBlockStart(1, `{"type":"text","text":""}`),
		anthTextDelta(1, "answer"),
		anthBlockStop(1),
		anthMsgDelta("end_turn", 40),
		anthMsgStop(),
	}}
	c := anthClient(tr)
	temp := 0.5

	s := c.Stream(context.Background(), Request{
		Model:       ClaudeSonnet45,
		Messages:    []Message{UserText("hard question")},
		Reasoning:   EffortMedium,
		MaxTokens:   100, // <= budget → bumped to budget+4096
		Temperature: &temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	body := tr.lastBody(t)
	if got := anthGet(t, body, "thinking", "type"); got != "enabled" {
		t.Errorf("thinking.type = %v", got)
	}
	if got := anthGet(t, body, "thinking", "budget_tokens"); got != float64(8192) {
		t.Errorf("budget_tokens = %v", got)
	}
	if _, has := body["temperature"]; has {
		t.Errorf("temperature must be omitted when thinking is enabled")
	}
	if got := body["max_tokens"]; got != float64(8192+4096) {
		t.Errorf("max_tokens = %v, want %v", got, 8192+4096)
	}

	want := []EventType{EventStart,
		EventThinkingStart, EventThinkingDelta, EventThinkingDelta, EventThinkingDelta, EventThinkingEnd,
		EventTextStart, EventTextDelta, EventTextEnd, EventDone}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Errorf("event types = %v, want %v", got, want)
	}
	if b := msg.Blocks[0]; b.Type != BlockThinking || b.Text != "Let me think" || b.Signature != "sig123" {
		t.Errorf("thinking block = %+v", b)
	}
	if b := msg.Blocks[1]; b.Type != BlockText || b.Text != "answer" {
		t.Errorf("text block = %+v", b)
	}
}

func TestAnthropicThinkingReplay(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	history := []Message{
		UserText("hard question"),
		{Role: RoleAssistant, Model: "claude-sonnet-4-5", Blocks: []Block{
			{Type: BlockThinking, Text: "hmm", Signature: "sig-1"},
			{Type: BlockThinking, Redacted: true, Signature: "opaque-redacted-data"},
			TextBlock("the answer"),
		}},
		UserText("go on"),
	}
	if _, err := c.Complete(context.Background(), Request{
		Model: ClaudeSonnet45, Messages: history, Reasoning: EffortMedium,
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	if got := anthGet(t, body, "messages", 1, "content", 0, "type"); got != "thinking" {
		t.Errorf("content[0].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 0, "thinking"); got != "hmm" {
		t.Errorf("thinking text = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 0, "signature"); got != "sig-1" {
		t.Errorf("signature = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 1, "type"); got != "redacted_thinking" {
		t.Errorf("content[1].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 1, "data"); got != "opaque-redacted-data" {
		t.Errorf("redacted data = %v", got)
	}
	if got := anthGet(t, body, "messages", 1, "content", 2, "type"); got != "text" {
		t.Errorf("content[2].type = %v", got)
	}
}

func TestAnthropicImageInput(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	if _, err := c.Complete(context.Background(), Request{
		Model: ClaudeSonnet45,
		Messages: []Message{UserBlocks(
			TextBlock("what is this?"),
			ImageBlock("image/png", []byte{1, 2, 3}),
		)},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	if got := anthGet(t, body, "messages", 0, "content", 1, "type"); got != "image" {
		t.Errorf("content[1].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "type"); got != "base64" {
		t.Errorf("source.type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "media_type"); got != "image/png" {
		t.Errorf("source.media_type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "data"); got != "AQID" {
		t.Errorf("source.data = %v", got)
	}
}

func TestAnthropicImageInToolResult(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	history := []Message{
		UserText("screenshot the page"),
		{Role: RoleAssistant, Model: "claude-sonnet-4-5", Blocks: []Block{
			{Type: BlockToolCall, ID: "call_1", Name: "screenshot", Args: json.RawMessage(`{}`)},
		}},
		ToolResultMessage("call_1", "screenshot", false,
			TextBlock("here it is"),
			ImageBlock("image/jpeg", []byte{9, 8, 7}),
		),
	}
	if _, err := c.Complete(context.Background(), Request{Model: ClaudeSonnet45, Messages: history}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	if got := anthGet(t, body, "messages", 2, "content", 0, "type"); got != "tool_result" {
		t.Errorf("type = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "content", 0, "text"); got != "here it is" {
		t.Errorf("nested text = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "content", 1, "type"); got != "image" {
		t.Errorf("nested image type = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "content", 1, "source", "media_type"); got != "image/jpeg" {
		t.Errorf("nested image media_type = %v", got)
	}
	if got := anthGet(t, body, "messages", 2, "content", 0, "content", 1, "source", "data"); got != "CQgH" {
		t.Errorf("nested image data = %v", got)
	}
}

func TestAnthropicMultiToolParallel(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		anthStart(`{"input_tokens":30,"output_tokens":1}`),
		anthBlockStart(0, `{"type":"text","text":""}`),
		anthTextDelta(0, "checking both"),
		anthBlockStop(0),
		anthBlockStart(1, `{"type":"tool_use","id":"tu_a","name":"tool_a","input":{}}`),
		anthBlockStart(2, `{"type":"tool_use","id":"tu_b","name":"tool_b","input":{}}`),
		anthInputJSONDelta(1, `{"x":`),
		anthInputJSONDelta(2, `{"y":`),
		anthInputJSONDelta(1, `1}`),
		anthInputJSONDelta(2, `2}`),
		anthBlockStop(1),
		anthBlockStop(2),
		anthMsgDelta("tool_use", 22),
		anthMsgStop(),
	}}
	c := anthClient(tr)

	s := c.Stream(context.Background(), Request{
		Model:    ClaudeSonnet45,
		Messages: []Message{UserText("run both tools")},
		Tools: []ToolDef{
			{Name: "tool_a", Description: "a", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "tool_b", Description: "b", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// Only the LAST tool def carries a breakpoint.
	body := tr.lastBody(t)
	if _, has := anthGet(t, body, "tools", 0).(map[string]any)["cache_control"]; has {
		t.Errorf("cache_control must only be on the last tool def")
	}
	if got := anthGet(t, body, "tools", 1, "cache_control", "type"); got != "ephemeral" {
		t.Errorf("last tool cache_control = %v", got)
	}

	// Index mapping: anthropic's index field flows through as Event.Index.
	type ti struct {
		typ EventType
		idx int
	}
	var toolEvents []ti
	for _, ev := range events {
		switch ev.Type {
		case EventToolCallStart, EventToolCallDelta, EventToolCallEnd:
			toolEvents = append(toolEvents, ti{ev.Type, ev.Index})
		}
	}
	want := []ti{
		{EventToolCallStart, 1}, {EventToolCallStart, 2},
		{EventToolCallDelta, 1}, {EventToolCallDelta, 2},
		{EventToolCallDelta, 1}, {EventToolCallDelta, 2},
		{EventToolCallEnd, 1}, {EventToolCallEnd, 2},
	}
	if !slices.Equal(toolEvents, want) {
		t.Errorf("tool events = %v, want %v", toolEvents, want)
	}

	if msg.StopReason != StopToolUse {
		t.Errorf("stop = %q", msg.StopReason)
	}
	if b := msg.Blocks[1]; b.ID != "tu_a" || b.Name != "tool_a" || string(b.Args) != `{"x":1}` {
		t.Errorf("block 1 = %+v", b)
	}
	if b := msg.Blocks[2]; b.ID != "tu_b" || b.Name != "tool_b" || string(b.Args) != `{"y":2}` {
		t.Errorf("block 2 = %+v", b)
	}
}

func TestAnthropicError400(t *testing.T) {
	errBody := `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens is too large"}}`
	tr := &captureTransport{status: 400, chunks: []string{errBody}}
	c := anthClient(tr)

	s := c.Stream(context.Background(), Request{Model: ClaudeSonnet45, Messages: []Message{UserText("hi")}})
	events := collectEvents(s)
	msg, err := s.Message()

	if err == nil {
		t.Fatal("want error")
	}
	if last := events[len(events)-1]; last.Type != EventError {
		t.Errorf("last event = %v", last.Type)
	}
	if msg.StopReason != StopError {
		t.Errorf("stop = %q", msg.StopReason)
	}
	// R8: the HTTP error body lands verbatim in ErrorText.
	if !strings.Contains(msg.ErrorText, "max_tokens is too large") {
		t.Errorf("ErrorText = %q, want provider body", msg.ErrorText)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Errorf("err = %v, want *HTTPError with status 400", err)
	}
}

func TestAnthropicAbortMidStream(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		anthStart(`{"input_tokens":10,"output_tokens":1}`),
		anthBlockStart(0, `{"type":"text","text":""}`),
		anthTextDelta(0, "Hello "),
		anthTextDelta(0, "world"),
		anthTextDelta(0, "!"),
		anthBlockStop(0),
		anthMsgDelta("end_turn", 5),
		anthMsgStop(),
	}}
	c := anthClient(tr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := c.Stream(ctx, Request{Model: ClaudeSonnet45, Messages: []Message{UserText("hi")}})
	n := 0
	for ev := range s.Events() {
		n++
		if ev.Type == EventTextDelta {
			cancel() // abort after the first delta; keep ranging to the terminal event
		}
	}
	msg, err := s.Message()
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if msg.StopReason != StopAborted {
		t.Errorf("stop = %q, want aborted", msg.StopReason)
	}
	if got := textOf(msg); got != "Hello " {
		t.Errorf("partial text = %q, want %q", got, "Hello ")
	}
	if n >= 8 {
		t.Errorf("stream did not stop early (%d events)", n)
	}
}

func TestAnthropicUnicode(t *testing.T) {
	// One SSE event split across two transport chunks in the middle of a
	// multi-byte rune: the emoji's UTF-8 bytes straddle the chunk boundary.
	full := anthTextDelta(0, "before🎉after")
	cut := strings.Index(full, "🎉") + 2 // inside the 4-byte emoji
	tr := &captureTransport{chunks: []string{
		anthStart(`{"input_tokens":5,"output_tokens":1}`),
		anthBlockStart(0, `{"type":"text","text":""}`),
		full[:cut],
		full[cut:],
		anthBlockStop(0),
		anthMsgDelta("end_turn", 3),
		anthMsgStop(),
	}}
	c := anthClient(tr)

	msg, err := c.Complete(context.Background(), Request{Model: ClaudeSonnet45, Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := textOf(msg); got != "before🎉after" {
		t.Errorf("text = %q", got)
	}
}

func TestAnthropicNoCache(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	if _, err := c.Complete(context.Background(), Request{
		Model:        ClaudeSonnet45,
		System:       "be nice",
		Messages:     []Message{UserText("hi")},
		Tools:        []ToolDef{{Name: "t", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
		DisableCache: true,
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body := tr.lastReq(t).Body; bytes.Contains(body, []byte("cache_control")) {
		t.Errorf("DisableCache payload contains cache_control:\n%s", body)
	}
}

func TestAnthropicNoAuth(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := New(WithTransport(tr), WithAPIKey("local", NoAuth))
	model := ClaudeSonnet45
	model.Provider = "local"
	model.BaseURL = "http://localhost:1234"

	if _, err := c.Complete(context.Background(), Request{Model: model, Messages: []Message{UserText("hi")}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	req := tr.lastReq(t)
	if req.URL != "http://localhost:1234/v1/messages" {
		t.Errorf("url = %q", req.URL)
	}
	if _, has := req.Header["X-Api-Key"]; has {
		t.Errorf("x-api-key must be omitted entirely for NoAuth, got %q", req.Header.Get("x-api-key"))
	}
}

func TestAnthropicCassetteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	req := Request{
		Model:     ClaudeSonnet45,
		System:    "be nice",
		Messages:  []Message{UserText("hi")},
		MaxTokens: 123,
	}

	// Record through the cassette recorder wrapping a scripted transport.
	scripted := &captureTransport{chunks: anthBasicChunks()}
	rec := cassette.Record(filepath.Join(dir, "testdata", "cassettes", "anth_roundtrip.json"), scripted)
	c1 := New(WithTransport(rec), WithAPIKey("anthropic", "test-key"))
	recorded, err := c1.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("recorded Complete: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("cassette close: %v", err)
	}

	// Replay resolves testdata/cassettes relative to the working directory.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatal(err)
		}
	}()

	c2 := New(WithTransport(cassette.Replay(t, "anth_roundtrip")), WithAPIKey("anthropic", "test-key"))
	replayed, err := c2.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed Complete: %v", err)
	}

	if textOf(replayed) != textOf(recorded) {
		t.Errorf("text: replayed %q, recorded %q", textOf(replayed), textOf(recorded))
	}
	if replayed.StopReason != recorded.StopReason {
		t.Errorf("stop: replayed %q, recorded %q", replayed.StopReason, recorded.StopReason)
	}
	if recorded.Usage == nil || replayed.Usage == nil || *replayed.Usage != *recorded.Usage {
		t.Errorf("usage: replayed %+v, recorded %+v", replayed.Usage, recorded.Usage)
	}
}

func TestAnthropicDocumentInput(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	if _, err := c.Complete(context.Background(), Request{
		Model: ClaudeSonnet45,
		Messages: []Message{UserBlocks(
			TextBlock("what does this say?"),
			DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3}),
		)},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	if got := anthGet(t, body, "messages", 0, "content", 1, "type"); got != "document" {
		t.Errorf("content[1].type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "type"); got != "base64" {
		t.Errorf("source.type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "media_type"); got != "application/pdf" {
		t.Errorf("source.media_type = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "source", "data"); got != "AQID" {
		t.Errorf("source.data = %v", got)
	}
	if got := anthGet(t, body, "messages", 0, "content", 1, "title"); got != "spec.pdf" {
		t.Errorf("title = %v", got)
	}
}

// TestAnthropicDocumentWithoutName omits the title rather than sending an
// empty one: the field is optional and a blank title is not a file name.
func TestAnthropicDocumentWithoutName(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	if _, err := c.Complete(context.Background(), Request{
		Model:    ClaudeSonnet45,
		Messages: []Message{UserBlocks(DocumentBlock("application/pdf", "", []byte{1, 2, 3}))},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	doc := anthGet(t, tr.lastBody(t), "messages", 0, "content", 0).(map[string]any)
	if doc["type"] != "document" {
		t.Fatalf("block = %v", doc)
	}
	if _, has := doc["title"]; has {
		t.Errorf("title = %v, want the field omitted", doc["title"])
	}
}

// TestAnthropicDocumentInToolResultDropped (R38): tool_result content carries
// text and images only, so a document placed there is dropped rather than
// sent as a block type the endpoint rejects.
func TestAnthropicDocumentInToolResultDropped(t *testing.T) {
	tr := &captureTransport{chunks: anthBasicChunks()}
	c := anthClient(tr)

	history := []Message{
		UserText("fetch the spec"),
		{Role: RoleAssistant, Model: "claude-sonnet-4-5", Blocks: []Block{
			{Type: BlockToolCall, ID: "call_1", Name: "fetch", Args: json.RawMessage(`{}`)},
		}},
		ToolResultMessage("call_1", "fetch", false,
			TextBlock("got it"),
			DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3}),
			ImageBlock("image/jpeg", []byte{9, 8, 7}),
		),
	}
	if _, err := c.Complete(context.Background(), Request{Model: ClaudeSonnet45, Messages: history}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := tr.lastBody(t)
	nested := anthGet(t, body, "messages", 2, "content", 0, "content").([]any)
	if len(nested) != 2 {
		t.Fatalf("tool_result content = %d blocks, want 2 (text, image): %v", len(nested), nested)
	}
	for i, raw := range nested {
		if got := raw.(map[string]any)["type"]; got == "document" {
			t.Errorf("tool_result content[%d] is a document; it must be dropped", i)
		}
	}
	if got := nested[0].(map[string]any)["text"]; got != "got it" {
		t.Errorf("nested text = %v", got)
	}
	if got := nested[1].(map[string]any)["type"]; got != "image" {
		t.Errorf("nested[1].type = %v, want image", got)
	}
}

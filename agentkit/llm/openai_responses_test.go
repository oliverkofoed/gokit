package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/cassette"
	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// ---- helpers (oair prefix per wiretest_test.go convention) ------------------

func oairModel() Model { return GPT5Mini }

func oairClient(tr transport.Interface) *Client {
	return New(WithTransport(tr), WithAPIKey("openai", "test-key"))
}

// oairRun streams req through tr, returning all events and the final message.
func oairRun(t *testing.T, tr transport.Interface, req Request) ([]Event, Message) {
	t.Helper()
	s := oairClient(tr).Stream(context.Background(), req)
	events := collectEvents(s)
	msg, _ := s.Message()
	return events, msg
}

func oairCreated() string {
	return sseChunk("response.created", `{"type":"response.created","response":{"id":"resp_1"}}`)
}

func oairCompleted(usage string) string {
	return sseChunk("response.completed", `{"type":"response.completed","response":{"usage":`+usage+`}}`)
}

// oairTextStream is a minimal happy-path text response.
func oairTextStream(deltas ...string) []string {
	chunks := []string{
		oairCreated(),
		sseChunk("response.output_item.added", `{"output_index":0,"item":{"id":"msg_1","type":"message"}}`),
	}
	var full strings.Builder
	for _, d := range deltas {
		full.WriteString(d)
		b, _ := json.Marshal(d)
		chunks = append(chunks, sseChunk("response.output_text.delta",
			`{"item_id":"msg_1","output_index":0,"delta":`+string(b)+`}`))
	}
	text, _ := json.Marshal(full.String())
	chunks = append(chunks,
		sseChunk("response.output_item.done",
			`{"output_index":0,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":`+string(text)+`}]}}`),
		oairCompleted(`{"input_tokens":10,"output_tokens":5,"input_tokens_details":{"cached_tokens":3}}`),
	)
	return chunks
}

func oairInputItems(t *testing.T, body map[string]any) []any {
	t.Helper()
	items, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("input is %T, want array; body: %v", body["input"], body)
	}
	return items
}

func oairItemAt(t *testing.T, items []any, i int) map[string]any {
	t.Helper()
	if i >= len(items) {
		t.Fatalf("input has %d items, want at least %d: %v", len(items), i+1, items)
	}
	m, ok := items[i].(map[string]any)
	if !ok {
		t.Fatalf("input[%d] is %T, want object", i, items[i])
	}
	return m
}

// ---- tests -------------------------------------------------------------------

func TestOpenAIResponsesBasicText(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("Hello ", "world")}
	req := Request{
		Model:     oairModel(),
		System:    "be concise",
		Messages:  []Message{UserText("hi")},
		MaxTokens: 512,
	}
	events, msg := oairRun(t, tr, req)

	// Payload golden.
	treq := tr.lastReq(t)
	if treq.Method != "POST" || treq.URL != "https://api.openai.com/v1/responses" {
		t.Fatalf("request = %s %s", treq.Method, treq.URL)
	}
	if got := treq.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q", got)
	}
	body := tr.lastBody(t)
	if body["model"] != "gpt-5-mini" {
		t.Errorf("model = %v", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if body["store"] != false {
		t.Errorf("store = %v", body["store"])
	}
	if !reflect.DeepEqual(body["include"], []any{"reasoning.encrypted_content"}) {
		t.Errorf("include = %v", body["include"])
	}
	if body["instructions"] != "be concise" {
		t.Errorf("instructions = %v", body["instructions"])
	}
	if body["max_output_tokens"] != float64(512) {
		t.Errorf("max_output_tokens = %v", body["max_output_tokens"])
	}
	for _, absent := range []string{"reasoning", "temperature", "tools"} {
		if _, ok := body[absent]; ok {
			t.Errorf("unexpected %q in body: %v", absent, body[absent])
		}
	}
	item := oairItemAt(t, oairInputItems(t, body), 0)
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("input[0] = %v", item)
	}
	content := item["content"].([]any)
	if !reflect.DeepEqual(content[0], map[string]any{"type": "input_text", "text": "hi"}) {
		t.Errorf("user content = %v", content[0])
	}

	// Event sequence + final message.
	want := []EventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventDone}
	if !reflect.DeepEqual(eventTypes(events), want) {
		t.Fatalf("events = %v, want %v", eventTypes(events), want)
	}
	if textOf(msg) != "Hello world" {
		t.Errorf("text = %q", textOf(msg))
	}
	if msg.StopReason != StopEnd {
		t.Errorf("stop = %q", msg.StopReason)
	}
	u := msg.Usage
	if u == nil || u.Input != 10 || u.Output != 5 || u.CacheRead != 3 {
		t.Fatalf("usage = %+v", u)
	}
	if u.TotalCost <= 0 {
		t.Errorf("total cost = %v", u.TotalCost)
	}
}

func TestOpenAIResponsesMaxTokensDefaultAndTemperature(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("ok")}
	temp := 0.25
	req := Request{Model: oairModel(), Messages: []Message{UserText("hi")}, Temperature: &temp}
	oairRun(t, tr, req)
	body := tr.lastBody(t)
	if body["max_output_tokens"] != float64(oairModel().MaxOutput) {
		t.Errorf("max_output_tokens = %v, want model default %d", body["max_output_tokens"], oairModel().MaxOutput)
	}
	if body["temperature"] != 0.25 {
		t.Errorf("temperature = %v", body["temperature"])
	}
	if _, ok := body["instructions"]; ok {
		t.Errorf("instructions should be omitted when System is empty")
	}
}

func TestOpenAIResponsesHeaders(t *testing.T) {
	t.Run("model_headers", func(t *testing.T) {
		tr := &captureTransport{chunks: oairTextStream("ok")}
		m := oairModel()
		m.Headers = map[string]string{"X-Custom": "yes"}
		oairRun(t, tr, Request{Model: m, Messages: []Message{UserText("hi")}})
		if got := tr.lastReq(t).Header.Get("X-Custom"); got != "yes" {
			t.Errorf("X-Custom = %q", got)
		}
	})
	t.Run("no_auth", func(t *testing.T) {
		tr := &captureTransport{chunks: oairTextStream("ok")}
		req := Request{Model: oairModel(), Messages: []Message{UserText("hi")}, APIKey: NoAuth}
		oairRun(t, tr, req)
		if _, ok := tr.lastReq(t).Header["Authorization"]; ok {
			t.Errorf("Authorization sent despite NoAuth: %v", tr.lastReq(t).Header)
		}
	})
}

func TestOpenAIResponsesToolCall(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		oairCreated(),
		sseChunk("response.output_item.added",
			`{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_9","name":"get_weather"}}`),
		sseChunk("response.function_call_arguments.delta",
			`{"item_id":"fc_1","output_index":0,"delta":"{\"city\":\"Cop"}`),
		sseChunk("response.function_call_arguments.delta",
			`{"item_id":"fc_1","output_index":0,"delta":"enhagen\"}"}`),
		sseChunk("response.output_item.done",
			`{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_9","name":"get_weather","arguments":"{\"city\":\"Copenhagen\"}"}}`),
		oairCompleted(`{"input_tokens":20,"output_tokens":7}`),
	}}
	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)
	req := Request{
		Model:    oairModel(),
		Messages: []Message{UserText("weather?")},
		Tools:    []ToolDef{{Name: "get_weather", Description: "Get weather", Schema: schema}},
	}

	s := oairClient(tr).Stream(context.Background(), req)
	var deltaCount int
	for ev := range s.Events() {
		if ev.Type == EventToolCallDelta {
			deltaCount++
			// R7: partial Args are always valid JSON while streaming.
			var v map[string]any
			if err := json.Unmarshal(ev.Message.Blocks[ev.Index].Args, &v); err != nil {
				t.Fatalf("partial Args invalid: %v (%s)", err, ev.Message.Blocks[ev.Index].Args)
			}
		}
	}
	if deltaCount != 2 {
		t.Fatalf("tool_call_delta count = %d, want 2", deltaCount)
	}
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// Tool definition golden.
	body := tr.lastBody(t)
	tools := body["tools"].([]any)
	wantTool := map[string]any{
		"type": "function", "name": "get_weather", "description": "Get weather",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		"strict":     false,
	}
	if !reflect.DeepEqual(tools[0], wantTool) {
		t.Errorf("tools[0] = %v\nwant     %v", tools[0], wantTool)
	}

	if msg.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", msg.StopReason)
	}
	b := msg.Blocks[0]
	if b.Type != BlockToolCall || b.ID != "call_9" || b.Name != "get_weather" {
		t.Fatalf("tool block = %+v", b)
	}
	var args map[string]string
	if err := json.Unmarshal(b.Args, &args); err != nil || args["city"] != "Copenhagen" {
		t.Fatalf("args = %s (err %v)", b.Args, err)
	}
}

func TestOpenAIResponsesToolResultRoundtrip(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("12 degrees")}
	req := Request{
		Model: oairModel(),
		Messages: []Message{
			UserText("weather in copenhagen?"),
			{Role: RoleAssistant, Model: "gpt-5-mini", Blocks: []Block{
				{Type: BlockToolCall, ID: "call_abc", Name: "get_weather", Args: json.RawMessage(`{"city":"Copenhagen"}`)},
			}},
			ToolResultMessage("call_abc", "get_weather", false, TextBlock("12°C, windy")),
		},
		Tools: []ToolDef{{Name: "get_weather", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
	}
	_, msg := oairRun(t, tr, req)
	if msg.StopReason != StopEnd {
		t.Fatalf("stop = %q (%s)", msg.StopReason, msg.ErrorText)
	}

	items := oairInputItems(t, tr.lastBody(t))
	if len(items) != 3 {
		t.Fatalf("input has %d items, want 3: %v", len(items), items)
	}
	fc := oairItemAt(t, items, 1)
	want := map[string]any{
		"type": "function_call", "call_id": "call_abc",
		"name": "get_weather", "arguments": `{"city":"Copenhagen"}`,
	}
	if !reflect.DeepEqual(fc, want) {
		t.Errorf("function_call = %v\nwant          %v", fc, want)
	}
	out := oairItemAt(t, items, 2)
	if out["type"] != "function_call_output" || out["call_id"] != "call_abc" || out["output"] != "12°C, windy" {
		t.Errorf("function_call_output = %v", out)
	}
}

func TestOpenAIResponsesReasoning(t *testing.T) {
	t.Run("param_mapping", func(t *testing.T) {
		for _, tc := range []struct {
			effort    Effort
			modelSupp bool
			want      string // "" = key absent
		}{
			{EffortHigh, true, "high"},
			{EffortLow, true, "low"},
			{EffortOff, true, ""},
			{EffortHigh, false, ""},
		} {
			tr := &captureTransport{chunks: oairTextStream("ok")}
			m := oairModel()
			m.Reasoning = tc.modelSupp
			oairRun(t, tr, Request{Model: m, Messages: []Message{UserText("hi")}, Reasoning: tc.effort})
			body := tr.lastBody(t)
			r, present := body["reasoning"]
			if tc.want == "" {
				if present {
					t.Errorf("effort=%q supported=%v: reasoning = %v, want absent", tc.effort, tc.modelSupp, r)
				}
				continue
			}
			if !reflect.DeepEqual(r, map[string]any{"effort": tc.want}) {
				t.Errorf("effort=%q: reasoning = %v, want effort %q", tc.effort, r, tc.want)
			}
		}
	})

	t.Run("stream", func(t *testing.T) {
		tr := &captureTransport{chunks: []string{
			oairCreated(),
			sseChunk("response.output_item.added", `{"output_index":0,"item":{"id":"rs_1","type":"reasoning"}}`),
			sseChunk("response.reasoning_summary_text.delta", `{"item_id":"rs_1","output_index":0,"delta":"Consider"}`),
			sseChunk("response.reasoning_summary_text.delta", `{"item_id":"rs_1","output_index":0,"delta":" the problem"}`),
			sseChunk("response.output_item.done",
				`{"output_index":0,"item":{"id":"rs_1","type":"reasoning","encrypted_content":"ENC123","summary":[{"type":"summary_text","text":"Consider the problem"}]}}`),
			sseChunk("response.output_item.added", `{"output_index":1,"item":{"id":"msg_1","type":"message"}}`),
			sseChunk("response.output_text.delta", `{"item_id":"msg_1","output_index":1,"delta":"Answer"}`),
			sseChunk("response.output_item.done",
				`{"output_index":1,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"Answer"}]}}`),
			oairCompleted(`{"input_tokens":5,"output_tokens":9}`),
		}}
		req := Request{Model: oairModel(), Messages: []Message{UserText("hi")}, Reasoning: EffortMedium}
		events, msg := oairRun(t, tr, req)

		want := []EventType{EventStart, EventThinkingStart, EventThinkingDelta, EventThinkingDelta,
			EventThinkingEnd, EventTextStart, EventTextDelta, EventTextEnd, EventDone}
		if !reflect.DeepEqual(eventTypes(events), want) {
			t.Fatalf("events = %v, want %v", eventTypes(events), want)
		}
		var end Event
		for _, ev := range events {
			if ev.Type == EventThinkingEnd {
				end = ev
			}
		}
		var sig oairSig
		if err := json.Unmarshal([]byte(end.Block.Signature), &sig); err != nil {
			t.Fatalf("thinking_end signature not JSON: %q (%v)", end.Block.Signature, err)
		}
		if sig.ID != "rs_1" || sig.EncryptedContent != "ENC123" {
			t.Fatalf("signature = %+v", sig)
		}
		if msg.Blocks[0].Type != BlockThinking || msg.Blocks[0].Text != "Consider the problem" {
			t.Errorf("thinking block = %+v", msg.Blocks[0])
		}
		if msg.Blocks[1].Type != BlockText || msg.Blocks[1].Text != "Answer" {
			t.Errorf("text block = %+v", msg.Blocks[1])
		}
	})
}

func TestOpenAIResponsesThinkingReplay(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("ok")}
	req := Request{
		Model: oairModel(),
		Messages: []Message{
			UserText("question"),
			{Role: RoleAssistant, Model: "gpt-5-mini", Blocks: []Block{
				{Type: BlockThinking, Text: "summary", Signature: `{"id":"rs_1","encrypted_content":"ENCDATA"}`},
				{Type: BlockThinking, Text: "unsigned thinking"}, // no Signature → dropped
				TextBlock("answer"),
			}},
			UserText("next question"),
		},
	}
	oairRun(t, tr, req)

	items := oairInputItems(t, tr.lastBody(t))
	if len(items) != 4 {
		t.Fatalf("input has %d items, want 4 (empty-signature thinking dropped): %v", len(items), items)
	}
	rs := oairItemAt(t, items, 1)
	// summary is required on replay even when empty: dropping it fails the
	// whole request with "Missing required parameter: 'input[N].summary'".
	want := map[string]any{
		"type": "reasoning", "id": "rs_1", "encrypted_content": "ENCDATA",
		"summary": []any{},
	}
	if !reflect.DeepEqual(rs, want) {
		t.Errorf("reasoning item = %v, want %v", rs, want)
	}
	at := oairItemAt(t, items, 2)
	if at["type"] != "message" || at["role"] != "assistant" {
		t.Fatalf("input[2] = %v", at)
	}
	if !reflect.DeepEqual(at["content"], []any{map[string]any{"type": "output_text", "text": "answer"}}) {
		t.Errorf("assistant content = %v", at["content"])
	}
	for i, it := range items {
		m := it.(map[string]any)
		if m["type"] == "reasoning" && i != 1 {
			t.Errorf("unexpected extra reasoning item at %d: %v", i, m)
		}
	}
}

func TestOpenAIResponsesImageInput(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("a cat")}
	img := ImageBlock("image/png", []byte{1, 2, 3, 4})
	req := Request{
		Model:    oairModel(),
		Messages: []Message{UserBlocks(TextBlock("what is this?"), img)},
	}
	oairRun(t, tr, req)

	item := oairItemAt(t, oairInputItems(t, tr.lastBody(t)), 0)
	content := item["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v", content)
	}
	want := map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + img.Data}
	if !reflect.DeepEqual(content[1], want) {
		t.Errorf("image part = %v\nwant       %v", content[1], want)
	}
}

func TestOpenAIResponsesImageInToolResult(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("done")}
	img := ImageBlock("image/jpeg", []byte{9, 9, 9})
	req := Request{
		Model: oairModel(),
		Messages: []Message{
			UserText("screenshot the page"),
			{Role: RoleAssistant, Model: "gpt-5-mini", Blocks: []Block{
				{Type: BlockToolCall, ID: "call_1", Name: "screenshot", Args: json.RawMessage(`{}`)},
			}},
			ToolResultMessage("call_1", "screenshot", false, TextBlock("captured"), img),
		},
	}
	oairRun(t, tr, req)

	items := oairInputItems(t, tr.lastBody(t))
	if len(items) != 4 {
		t.Fatalf("input has %d items, want 4: %v", len(items), items)
	}
	out := oairItemAt(t, items, 2)
	if out["type"] != "function_call_output" || out["call_id"] != "call_1" || out["output"] != "captured" {
		t.Fatalf("function_call_output = %v", out)
	}
	spill := oairItemAt(t, items, 3)
	if spill["type"] != "message" || spill["role"] != "user" {
		t.Fatalf("spillover = %v, want user message", spill)
	}
	want := []any{map[string]any{"type": "input_image", "image_url": "data:image/jpeg;base64," + img.Data}}
	if !reflect.DeepEqual(spill["content"], want) {
		t.Errorf("spillover content = %v\nwant             %v", spill["content"], want)
	}
}

func TestOpenAIResponsesMultiToolParallel(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		oairCreated(),
		sseChunk("response.output_item.added",
			`{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather"}}`),
		sseChunk("response.output_item.added",
			`{"output_index":1,"item":{"id":"fc_2","type":"function_call","call_id":"call_2","name":"get_time"}}`),
		// Interleaved argument deltas across the two calls.
		sseChunk("response.function_call_arguments.delta", `{"item_id":"fc_1","output_index":0,"delta":"{\"city\":"}`),
		sseChunk("response.function_call_arguments.delta", `{"item_id":"fc_2","output_index":1,"delta":"{\"tz\":\"CET\"}"}`),
		sseChunk("response.function_call_arguments.delta", `{"item_id":"fc_1","output_index":0,"delta":"\"Aarhus\"}"}`),
		sseChunk("response.output_item.done",
			`{"output_index":1,"item":{"id":"fc_2","type":"function_call","call_id":"call_2","name":"get_time","arguments":"{\"tz\":\"CET\"}"}}`),
		sseChunk("response.output_item.done",
			`{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Aarhus\"}"}}`),
		oairCompleted(`{"input_tokens":30,"output_tokens":12}`),
	}}
	req := Request{Model: oairModel(), Messages: []Message{UserText("both please")}}
	events, msg := oairRun(t, tr, req)

	// Index mapping: fc_1 → 0, fc_2 → 1, interleaved.
	for _, ev := range events {
		switch ev.Type {
		case EventToolCallStart, EventToolCallDelta, EventToolCallEnd:
			id := ev.Message.Blocks[ev.Index].ID
			if (ev.Index == 0) != (id == "call_1") {
				t.Errorf("%s index %d routed to block ID %q", ev.Type, ev.Index, id)
			}
		}
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q (%s)", msg.StopReason, msg.ErrorText)
	}
	if len(msg.Blocks) != 2 {
		t.Fatalf("blocks = %+v", msg.Blocks)
	}
	b0, b1 := msg.Blocks[0], msg.Blocks[1]
	if b0.ID != "call_1" || b0.Name != "get_weather" || string(b0.Args) != `{"city":"Aarhus"}` {
		t.Errorf("block 0 = %+v", b0)
	}
	if b1.ID != "call_2" || b1.Name != "get_time" || string(b1.Args) != `{"tz":"CET"}` {
		t.Errorf("block 1 = %+v", b1)
	}
}

func TestOpenAIResponsesStopLength(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		oairCreated(),
		sseChunk("response.output_item.added", `{"output_index":0,"item":{"id":"msg_1","type":"message"}}`),
		sseChunk("response.output_text.delta", `{"item_id":"msg_1","output_index":0,"delta":"truncat"}`),
		sseChunk("response.output_item.done", `{"output_index":0,"item":{"id":"msg_1","type":"message"}}`),
		sseChunk("response.completed",
			`{"response":{"usage":{"input_tokens":4,"output_tokens":16},"incomplete_details":{"reason":"max_output_tokens"}}}`),
	}}
	_, msg := oairRun(t, tr, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})
	if msg.StopReason != StopLength {
		t.Fatalf("stop = %q, want length", msg.StopReason)
	}
	if textOf(msg) != "truncat" {
		t.Errorf("text = %q", textOf(msg))
	}
}

func TestOpenAIResponsesError400(t *testing.T) {
	tr := &captureTransport{
		status: 400,
		chunks: []string{`{"error":{"message":"invalid 'input' item","type":"invalid_request_error"}}`},
	}
	events, msg := oairRun(t, tr, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})

	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("last event = %v", last.Type)
	}
	var httpErr *HTTPError
	if !errors.As(last.Err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("err = %v, want *HTTPError 400", last.Err)
	}
	if msg.StopReason != StopError {
		t.Errorf("stop = %q", msg.StopReason)
	}
	if !strings.Contains(msg.ErrorText, "invalid 'input' item") {
		t.Errorf("ErrorText = %q, want provider body verbatim", msg.ErrorText)
	}
}

func TestOpenAIResponsesFailedEvent(t *testing.T) {
	t.Run("response_failed", func(t *testing.T) {
		tr := &captureTransport{chunks: []string{
			oairCreated(),
			sseChunk("response.failed", `{"response":{"error":{"message":"model overloaded","code":"server_error"}}}`),
		}}
		_, msg := oairRun(t, tr, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})
		if msg.StopReason != StopError || !strings.Contains(msg.ErrorText, "model overloaded") {
			t.Fatalf("stop = %q, error = %q", msg.StopReason, msg.ErrorText)
		}
	})
	t.Run("error_event", func(t *testing.T) {
		tr := &captureTransport{chunks: []string{
			oairCreated(),
			sseChunk("error", `{"type":"error","code":"rate_limit","message":"slow down"}`),
		}}
		_, msg := oairRun(t, tr, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})
		if msg.StopReason != StopError || !strings.Contains(msg.ErrorText, "slow down") {
			t.Fatalf("stop = %q, error = %q", msg.StopReason, msg.ErrorText)
		}
	})
}

func TestOpenAIResponsesAbortMidStream(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("Hello ", "world")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := oairClient(tr).Stream(ctx, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})
	var last Event
	for ev := range s.Events() {
		last = ev
		if ev.Type == EventTextDelta {
			cancel() // the transport stops serving chunks; the stream must end in an error event
		}
	}
	if last.Type != EventError {
		t.Fatalf("last event = %v, want error", last.Type)
	}
	if !errors.Is(last.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", last.Err)
	}
	msg, err := s.Message()
	if err == nil || msg.StopReason != StopAborted {
		t.Fatalf("stop = %q (err %v), want aborted", msg.StopReason, err)
	}
	if textOf(msg) != "Hello " {
		t.Errorf("partial text = %q, want %q", textOf(msg), "Hello ")
	}
}

func TestOpenAIResponsesUnicodeSplitChunks(t *testing.T) {
	const text = "héllo 🎉 wörld"
	full := strings.Join(oairTextStream(text), "")
	// Split the transport stream in the middle of the emoji's UTF-8 bytes.
	at := strings.Index(full, "🎉") + 2
	tr := &captureTransport{chunks: []string{full[:at], full[at:]}}

	_, msg := oairRun(t, tr, Request{Model: oairModel(), Messages: []Message{UserText("hi")}})
	if msg.StopReason != StopEnd {
		t.Fatalf("stop = %q (%s)", msg.StopReason, msg.ErrorText)
	}
	if textOf(msg) != text {
		t.Fatalf("text = %q, want %q", textOf(msg), text)
	}
}

func TestOpenAIResponsesCassetteRoundTrip(t *testing.T) {
	// go.mod targets 1.23, so t.Chdir (1.24) is off-limits.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	const name = "oair_roundtrip"
	req := Request{
		Model:     oairModel(),
		System:    "be brief",
		Messages:  []Message{UserText("hi")},
		MaxTokens: 128,
	}

	// Record: scripted transport plays the provider; the recorder writes the
	// cassette (into the temp working directory, never committed).
	scripted := &captureTransport{chunks: oairTextStream("Hi ", "there")}
	rec := cassette.Record(filepath.Join("testdata", "cassettes", name+".json"), scripted)
	recorded, err := New(WithTransport(rec), WithAPIKey("openai", "test-key")).Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("recorded run: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("write cassette: %v", err)
	}

	// Replay: zero network, same request must reproduce the same message.
	replayed, err := New(WithTransport(cassette.Replay(t, name)), WithAPIKey("openai", "test-key")).Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed run: %v", err)
	}
	if textOf(replayed) != "Hi there" || textOf(replayed) != textOf(recorded) {
		t.Fatalf("replayed text = %q, recorded %q", textOf(replayed), textOf(recorded))
	}
	if replayed.StopReason != recorded.StopReason {
		t.Fatalf("stop: replayed %q, recorded %q", replayed.StopReason, recorded.StopReason)
	}
	if fmt.Sprintf("%+v", replayed.Usage) != fmt.Sprintf("%+v", recorded.Usage) {
		t.Fatalf("usage: replayed %+v, recorded %+v", replayed.Usage, recorded.Usage)
	}
}

// TestOpenAIResponsesInStreamError covers the /responses quirk that some
// failures arrive as an SSE error event inside an HTTP 200 rather than a
// non-2xx status — quota exhaustion is one. There is no *HTTPError to
// classify on and R12 never retries, so the provider's error code is the
// only machine-readable signal and must survive into the message.
//
// The body is the one api.openai.com actually returned when the account ran
// out of credits; chat completions reported the same condition as a real
// HTTP 429, which is why the two protocols need separate handling.
func TestOpenAIResponsesInStreamError(t *testing.T) {
	tr := &captureTransport{chunks: []string{
		sseChunk("error", `{"type":"error","code":"credit_balance_exhausted",`+
			`"message":"You have no credits remaining. Add credits to continue using the API."}`),
	}}
	c := New(WithTransport(tr), WithAPIKey("openai", "test-key"), WithRetry(0))

	s := c.Stream(context.Background(), Request{
		Model:    GPT5Mini,
		Messages: []Message{UserText("hi")},
	})
	events := collectEvents(s)
	msg, err := s.Message()

	if err == nil {
		t.Fatal("want an error")
	}
	// Deliberately NOT an *HTTPError: the transport saw a 200.
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		t.Errorf("err = %v, want a plain error (the wire status was 200)", err)
	}
	if !strings.Contains(err.Error(), "credit_balance_exhausted") {
		t.Errorf("err = %v, want the provider error code preserved", err)
	}
	if !strings.Contains(msg.ErrorText, "no credits remaining") {
		t.Errorf("ErrorText = %q, want the provider message", msg.ErrorText)
	}
	if msg.StopReason != StopError {
		t.Errorf("stop = %q, want %q", msg.StopReason, StopError)
	}
	if last := events[len(events)-1]; last.Type != EventError {
		t.Errorf("last event = %v, want %v", last.Type, EventError)
	}
}

func TestOpenAIResponsesDocumentInput(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("it is a spec")}
	doc := DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3, 4})
	req := Request{
		Model:    oairModel(),
		Messages: []Message{UserBlocks(TextBlock("what does this say?"), doc)},
	}
	oairRun(t, tr, req)

	item := oairItemAt(t, oairInputItems(t, tr.lastBody(t)), 0)
	content := item["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v", content)
	}
	want := map[string]any{
		"type":      "input_file",
		"file_data": "data:application/pdf;base64," + doc.Data,
		"filename":  "spec.pdf",
	}
	if !reflect.DeepEqual(content[1], want) {
		t.Errorf("document part = %v\nwant          %v", content[1], want)
	}
}

// TestOpenAIResponsesDocumentWithoutName omits the filename rather than
// sending an empty one.
func TestOpenAIResponsesDocumentWithoutName(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("ok")}
	doc := DocumentBlock("application/pdf", "", []byte{1, 2, 3})
	oairRun(t, tr, Request{
		Model:    oairModel(),
		Messages: []Message{UserBlocks(doc)},
	})

	item := oairItemAt(t, oairInputItems(t, tr.lastBody(t)), 0)
	part := item["content"].([]any)[0].(map[string]any)
	if _, has := part["filename"]; has {
		t.Errorf("filename = %v, want the field omitted", part["filename"])
	}
	if part["file_data"] != "data:application/pdf;base64,"+doc.Data {
		t.Errorf("file_data = %v", part["file_data"])
	}
}

// TestOpenAIResponsesDocumentInToolResultDropped (R38): function_call_output
// carries text only and images spill into a following user message; a
// document has no such home on this protocol and is dropped.
func TestOpenAIResponsesDocumentInToolResultDropped(t *testing.T) {
	tr := &captureTransport{chunks: oairTextStream("done")}
	oairRun(t, tr, Request{
		Model: oairModel(),
		Messages: []Message{
			UserText("fetch the spec"),
			{Role: RoleAssistant, Model: "gpt-5-mini", Blocks: []Block{
				{Type: BlockToolCall, ID: "call_1", Name: "fetch", Args: json.RawMessage(`{}`)},
			}},
			ToolResultMessage("call_1", "fetch", false,
				TextBlock("got it"), DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2, 3})),
		},
	})

	items := oairInputItems(t, tr.lastBody(t))
	if len(items) != 3 {
		t.Fatalf("input has %d items, want 3 with no spillover: %v", len(items), items)
	}
	out := oairItemAt(t, items, 2)
	if out["type"] != "function_call_output" || out["output"] != "got it" {
		t.Fatalf("function_call_output = %v", out)
	}
	if strings.Contains(string(tr.lastReq(t).Body), "AQID") {
		t.Error("document bytes reached the wire from a tool result")
	}
}

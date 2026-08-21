package llm

// Tests for message/model types (SPEC R1, R3, R4).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"testing"
	"time"
)

var msgTestTime = time.Date(2026, 7, 8, 9, 30, 15, 123456789, time.UTC)

// roundTripJSON marshals v, unmarshals into a fresh T, re-marshals, and
// requires byte-identical output.
func roundTripJSON[T any](t *testing.T, v T) {
	t.Helper()
	b1, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var v2 T
	if err := json.Unmarshal(b1, &v2); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, b1)
	}
	b2, err := json.Marshal(v2)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("round trip not byte-identical:\nfirst:  %s\nsecond: %s", b1, b2)
	}
}

// TestMessageJSONRoundTrip: Model, Message, and Block values survive
// marshal→unmarshal→marshal byte-identically (R1, R3).
func TestMessageJSONRoundTrip(t *testing.T) {
	usage := &Usage{Input: 1200, Output: 340, CacheRead: 900, CacheWrite: 100, TotalCost: 0.004321}

	models := map[string]Model{
		"catalog sonnet": ClaudeSonnet45,
		"catalog gemini": Gemini25Flash,
		"openrouter":     OpenRouter("meta-llama/llama-3-70b"),
		"kitchen sink": {
			ID: "custom", Name: "Custom", API: OpenAIChat, Provider: "my-proxy",
			BaseURL:       "http://localhost:8080/v1",
			Cost:          Cost{Input: 0.1, Output: 0.2, CacheRead: 0.01, CacheWrite: 0.02},
			ContextWindow: 32_000, MaxOutput: 4_096,
			Reasoning: true, Vision: false,
			Headers: map[string]string{"X-Extra": "1"},
			Quirks: Quirks{
				MaxTokensField: "max_tokens", NoStreamUsage: true,
				NoReasoningEffort: true, AnthropicCacheControl: true,
			},
		},
	}
	for name, m := range models {
		t.Run("model "+name, func(t *testing.T) { roundTripJSON(t, m) })
	}

	messages := map[string]Message{
		"user text": {
			Role: RoleUser, Blocks: []Block{TextBlock("hello")}, Time: msgTestTime,
		},
		"user image": {
			Role:   RoleUser,
			Blocks: []Block{TextBlock("look:"), ImageBlock("image/png", []byte{1, 2, 3, 255})},
			Time:   msgTestTime,
		},
		"assistant all blocks": {
			Role: RoleAssistant,
			Blocks: []Block{
				{Type: BlockThinking, Text: "hmm", Signature: "sig-1"},
				{Type: BlockThinking, Redacted: true, Signature: "opaque-redacted"},
				{Type: BlockText, Text: "answer with unicode é😀"},
				{Type: BlockToolCall, ID: "call_1", Name: "search", Args: json.RawMessage(`{"q":"go","n":2}`)},
				{Type: BlockImage, MimeType: "image/jpeg", Data: "AQID"},
			},
			Time:       msgTestTime,
			Model:      "claude-sonnet-4-5",
			Provider:   "anthropic",
			API:        AnthropicMessages,
			StopReason: StopToolUse,
			Usage:      usage,
		},
		"assistant error": {
			Role: RoleAssistant, Blocks: []Block{TextBlock("partial")}, Time: msgTestTime,
			Model: "gpt-5", Provider: "openai", API: OpenAIResponses,
			StopReason: StopError, ErrorText: "http 500: boom",
			Usage: &Usage{Input: 10},
		},
		"tool result": {
			Role:       RoleToolResult,
			Blocks:     []Block{TextBlock("42"), ImageBlock("image/png", []byte("img"))},
			Time:       msgTestTime,
			ToolCallID: "call_1", ToolName: "search", IsError: true,
		},
		"kind message": {
			Kind: "compaction-marker",
			Meta: json.RawMessage(`{"dropped":17,"ids":[1,2,3]}`),
			Time: msgTestTime,
		},
		"kind with role and blocks": {
			Role: RoleUser, Kind: "ui-note", Blocks: []Block{TextBlock("shown, never sent")},
			Meta: json.RawMessage(`"free-form"`), Time: msgTestTime,
		},
	}
	for name, m := range messages {
		t.Run("message "+name, func(t *testing.T) { roundTripJSON(t, m) })
	}

	// Args byte preservation: json.RawMessage keeps compact-but-exotic
	// encodings (key order, non-ASCII) verbatim through Message round trips.
	// (encoding/json compacts RawMessage on marshal, so interior whitespace
	// is normalized — key order and content bytes are the guarantee.)
	weird := Message{
		Role: RoleAssistant, Time: msgTestTime,
		Blocks: []Block{{Type: BlockToolCall, ID: "c", Name: "n",
			Args: json.RawMessage(`{"b":1,"a":"é"}`)}},
	}
	b, _ := json.Marshal(weird)
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if string(back.Blocks[0].Args) != `{"b":1,"a":"é"}` {
		t.Errorf("Args not preserved verbatim: %s", back.Blocks[0].Args)
	}
}

// TestMessageConstructors: every constructor sets Time (R3) and fills its
// fields.
func TestMessageConstructors(t *testing.T) {
	before := time.Now()

	u := UserText("hi")
	if u.Role != RoleUser || len(u.Blocks) != 1 || u.Blocks[0].Type != BlockText || u.Blocks[0].Text != "hi" {
		t.Errorf("UserText = %+v", u)
	}
	ub := UserBlocks(TextBlock("a"), ImageBlock("image/png", []byte{9}))
	if ub.Role != RoleUser || len(ub.Blocks) != 2 {
		t.Errorf("UserBlocks = %+v", ub)
	}
	tr := ToolResultMessage("id1", "grep", true, TextBlock("no match"))
	if tr.Role != RoleToolResult || tr.ToolCallID != "id1" || tr.ToolName != "grep" || !tr.IsError || len(tr.Blocks) != 1 {
		t.Errorf("ToolResultMessage = %+v", tr)
	}
	am := AppMessage("divider", json.RawMessage(`{"x":1}`))
	if am.Kind != "divider" || string(am.Meta) != `{"x":1}` || am.Role != "" || am.Blocks != nil {
		t.Errorf("AppMessage = %+v", am)
	}

	after := time.Now()
	for name, m := range map[string]Message{"UserText": u, "UserBlocks": ub, "ToolResultMessage": tr, "AppMessage": am} {
		if m.Time.IsZero() || m.Time.Before(before) || m.Time.After(after) {
			t.Errorf("%s: Time = %v not set to now (window %v..%v)", name, m.Time, before, after)
		}
	}

	if tb := TextBlock("t"); tb.Type != BlockText || tb.Text != "t" {
		t.Errorf("TextBlock = %+v", tb)
	}
	ib := ImageBlock("image/png", []byte{1, 2, 3})
	if ib.Type != BlockImage || ib.MimeType != "image/png" || ib.Data != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Errorf("ImageBlock = %+v", ib)
	}
}

// TestComputeCost: R4 math, including cache read/write and zero-cost models.
func TestComputeCost(t *testing.T) {
	cheap := Model{Cost: Cost{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.25}}
	cases := []struct {
		name  string
		model Model
		usage Usage
		want  float64
	}{
		{"zero usage", ClaudeSonnet45, Usage{}, 0},
		{"zero-cost model", OpenRouter("free/model"), Usage{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 1e6}, 0},
		{"one million of everything", ClaudeSonnet45,
			Usage{Input: 1_000_000, Output: 1_000_000, CacheRead: 1_000_000, CacheWrite: 1_000_000},
			3 + 15 + 0.30 + 3.75},
		{"mixed", cheap, Usage{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100},
			(1000*1.0 + 500*2.0 + 200*0.5 + 100*0.25) / 1e6},
		{"input only", cheap, Usage{Input: 2_000_000}, 2},
		{"cache only", cheap, Usage{CacheRead: 4_000_000, CacheWrite: 4_000_000}, 2 + 1},
		{"TotalCost field ignored as input", cheap, Usage{Input: 1_000_000, TotalCost: 12345}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeCost(tc.model, tc.usage)
			if math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("ComputeCost = %v, want %v", got, tc.want)
			}
		})
	}
}

package llm

// Cross-provider handoff (SPEC §12.2 handoff_from_*): history recorded by one
// provider must encode cleanly for every other protocol, with thinking blocks
// textified (R17), signatures dropped, and tool call/result pairing intact
// (R16/R18). Only the outgoing payload matters here, so the scripted response
// is a plain 400 and the stream result is ignored.

import (
	"context"
	"strings"
	"testing"
)

// hoHistory is a conversation as recorded from producedBy: thinking with a
// signature, text, a tool call, its result, and a follow-up user message.
func hoHistory(producedBy Model) []Message {
	return []Message{
		UserText("q"),
		{
			Role:  RoleAssistant,
			Model: producedBy.ID, Provider: producedBy.Provider, API: producedBy.API,
			StopReason: StopToolUse,
			Blocks: []Block{
				{Type: BlockThinking, Text: "let me think", Signature: `hosig-opaque-123`},
				{Type: BlockText, Text: "answer"},
				{Type: BlockToolCall, ID: "toolu_01AB", Name: "f", Args: []byte(`{"x":1}`)},
			},
		},
		ToolResultMessage("toolu_01AB", "f", false, TextBlock("42")),
		UserText("next"),
	}
}

// hoHasThinkingTag reports whether a payload carries the <thinking> handoff
// tag in either raw or JSON-HTML-escaped (<) form.
func hoHasThinkingTag(body string) bool {
	return strings.Contains(body, "<thinking>") || strings.Contains(body, `\u003cthinking\u003e`)
}

// hoPayload encodes history for target and returns the raw request body.
func hoPayload(t *testing.T, target Model, history []Message) string {
	t.Helper()
	tr := &captureTransport{status: 400, chunks: nil}
	client := New(WithTransport(tr), WithAPIKey(target.Provider, "test-key"), WithRetry(0))
	_, _ = client.Complete(context.Background(), Request{
		Model:    target,
		System:   "sys",
		Messages: history,
		Tools:    []ToolDef{{Name: "f", Description: "d", Schema: []byte(`{"type":"object"}`)}},
	})
	return string(tr.lastReq(t).Body)
}

func TestHandoffFromAnthropic(t *testing.T) {
	history := hoHistory(ClaudeSonnet45)
	for _, target := range []Model{GPT5Mini, OpenRouter("x-ai/grok-code-fast-1"), Gemini25Flash} {
		t.Run(string(target.API), func(t *testing.T) {
			body := hoPayload(t, target, history)
			if !hoHasThinkingTag(body) {
				t.Errorf("cross-model thinking not textified (R17):\n%s", body)
			}
			if strings.Contains(body, "hosig-opaque-123") {
				t.Errorf("foreign signature leaked to the wire:\n%s", body)
			}
			// The tool exchange survives: call name and result content present.
			if !strings.Contains(body, `"f"`) || !strings.Contains(body, "42") {
				t.Errorf("tool call/result pairing lost:\n%s", body)
			}
		})
	}
}

func TestHandoffSameModelKeepsThinking(t *testing.T) {
	body := hoPayload(t, ClaudeSonnet45, hoHistory(ClaudeSonnet45))
	if hoHasThinkingTag(body) {
		t.Errorf("same-model thinking wrongly textified:\n%s", body)
	}
	if !strings.Contains(body, `"thinking"`) || !strings.Contains(body, "hosig-opaque-123") {
		t.Errorf("native thinking replay missing:\n%s", body)
	}
}

func TestHandoffFromOpenAIToAnthropic(t *testing.T) {
	history := hoHistory(GPT5Mini)
	// OpenAI Responses signatures are JSON blobs; they too must not leak.
	history[1].Blocks[0].Signature = `{"id":"rs_1","encrypted_content":"hoenc-xyz"}`
	body := hoPayload(t, ClaudeSonnet45, history)
	if !hoHasThinkingTag(body) {
		t.Errorf("thinking not textified for anthropic:\n%s", body)
	}
	if strings.Contains(body, "hoenc-xyz") {
		t.Errorf("encrypted reasoning leaked:\n%s", body)
	}
}

func TestHandoffGeminiEmptyCallIDs(t *testing.T) {
	// Gemini can produce empty tool-call IDs; replay to protocols that
	// require IDs must synthesize them (R18).
	history := []Message{
		UserText("q"),
		{
			Role:  RoleAssistant,
			Model: Gemini25Flash.ID, Provider: "google", API: GoogleGemini,
			StopReason: StopToolUse,
			Blocks: []Block{
				{Type: BlockToolCall, ID: "", Name: "f", Args: []byte(`{"x":1}`)},
			},
		},
		ToolResultMessage("", "f", false, TextBlock("42")),
		UserText("next"),
	}
	body := hoPayload(t, GPT5Mini, history)
	if !strings.Contains(body, `"call_1"`) {
		t.Errorf("empty gemini call id not synthesized (R18):\n%s", body)
	}
}

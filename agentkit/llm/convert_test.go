package llm

// Tests for history normalization (SPEC §5.3: R16–R20, plus R37 step 0 and
// the R5 no-mutation guarantee).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var convertTestModel = Model{
	ID: "target-model", API: OpenAIChat, Provider: "testprov", Vision: true,
}

// convertAssistant builds an assistant message attributed to a model ID.
func convertAssistant(modelID string, blocks ...Block) Message {
	return Message{Role: RoleAssistant, Model: modelID, Blocks: blocks, Time: time.Now()}
}

func toolCallBlock(id, name string) Block {
	return Block{Type: BlockToolCall, ID: id, Name: name, Args: json.RawMessage(`{}`)}
}

const orphanText = "tool call was interrupted; no result available"

func assertSyntheticResult(t *testing.T, m Message, callID, toolName string) {
	t.Helper()
	if m.Role != RoleToolResult || m.ToolCallID != callID || m.ToolName != toolName {
		t.Fatalf("synthetic result = %+v, want tool_result for %s/%s", m, callID, toolName)
	}
	if !m.IsError {
		t.Error("synthetic result must have IsError: true")
	}
	if len(m.Blocks) != 1 || m.Blocks[0].Type != BlockText || m.Blocks[0].Text != orphanText {
		t.Errorf("synthetic result blocks = %+v, want single text %q", m.Blocks, orphanText)
	}
}

// TestConvertKindRemoval (R37 step 0): Kind messages are removed before any
// other rule — an orphaned tool call followed only by a Kind message still
// gets its synthetic result, and the Kind message is gone.
func TestConvertKindRemoval(t *testing.T) {
	asst := convertAssistant(convertTestModel.ID, toolCallBlock("a1", "grep"))
	in := []Message{
		UserText("hi"),
		asst,
		AppMessage("ui-divider", json.RawMessage(`{"z":1}`)),
	}
	out := normalizeMessages(convertTestModel, in)

	for _, m := range out {
		if m.Kind != "" {
			t.Fatalf("Kind message leaked into normalized history: %+v", m)
		}
	}
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3 (user, assistant, synthetic result); got %+v", len(out), out)
	}
	if out[1].Role != RoleAssistant {
		t.Fatalf("out[1] = %+v", out[1])
	}
	assertSyntheticResult(t, out[2], "a1", "grep")
	if !out[2].Time.Equal(asst.Time) {
		t.Errorf("synthetic result Time = %v, want assistant's %v", out[2].Time, asst.Time)
	}
}

// TestConvertOrphanInjection (R16): unanswered call IDs among answered ones
// get synthetic error results, placed after the existing result cluster, in
// call order.
func TestConvertOrphanInjection(t *testing.T) {
	in := []Message{
		UserText("go"),
		convertAssistant(convertTestModel.ID,
			toolCallBlock("a", "alpha"),
			toolCallBlock("b", "beta"),
			toolCallBlock("c", "gamma"),
		),
		ToolResultMessage("b", "beta", false, TextBlock("beta result")),
		UserText("next"),
	}
	out := normalizeMessages(convertTestModel, in)

	if len(out) != 6 {
		t.Fatalf("len(out) = %d, want 6; got %+v", len(out), out)
	}
	if out[2].Role != RoleToolResult || out[2].ToolCallID != "b" || out[2].IsError {
		t.Fatalf("existing result must stay first in the cluster: %+v", out[2])
	}
	assertSyntheticResult(t, out[3], "a", "alpha")
	assertSyntheticResult(t, out[4], "c", "gamma")
	if out[5].Role != RoleUser {
		t.Fatalf("trailing user message displaced: %+v", out[5])
	}

	t.Run("fully answered calls get no synthetics", func(t *testing.T) {
		in := []Message{
			convertAssistant(convertTestModel.ID, toolCallBlock("x", "t")),
			ToolResultMessage("x", "t", false, TextBlock("ok")),
		}
		out := normalizeMessages(convertTestModel, in)
		if len(out) != 2 {
			t.Fatalf("len(out) = %d, want 2; got %+v", len(out), out)
		}
	})
}

// TestConvertCrossModelThinking (R17): thinking from another model becomes
// <thinking>-wrapped text with the signature dropped; redacted thinking is
// dropped entirely; same-model messages are preserved for native replay.
func TestConvertCrossModelThinking(t *testing.T) {
	t.Run("cross-model", func(t *testing.T) {
		in := []Message{convertAssistant("other-model",
			Block{Type: BlockThinking, Text: "deep thought", Signature: "sig-1"},
			Block{Type: BlockThinking, Redacted: true, Signature: "opaque"},
			TextBlock("answer"),
		)}
		out := normalizeMessages(convertTestModel, in)
		if len(out) != 1 || len(out[0].Blocks) != 2 {
			t.Fatalf("out = %+v, want one message with 2 blocks", out)
		}
		b0 := out[0].Blocks[0]
		if b0.Type != BlockText {
			t.Fatalf("converted thinking block type = %q, want text", b0.Type)
		}
		if b0.Text != "<thinking>\ndeep thought\n</thinking>\n" {
			t.Errorf("converted text = %q", b0.Text)
		}
		if b0.Signature != "" {
			t.Errorf("signature must be dropped on handoff, got %q", b0.Signature)
		}
		if out[0].Blocks[1].Text != "answer" {
			t.Errorf("trailing text block lost: %+v", out[0].Blocks)
		}
	})

	t.Run("same-model preserved", func(t *testing.T) {
		in := []Message{convertAssistant(convertTestModel.ID,
			Block{Type: BlockThinking, Text: "deep thought", Signature: "sig-1"},
			Block{Type: BlockThinking, Redacted: true, Signature: "opaque"},
			TextBlock("answer"),
		)}
		out := normalizeMessages(convertTestModel, in)
		if len(out) != 1 || len(out[0].Blocks) != 3 {
			t.Fatalf("out = %+v, want all 3 blocks preserved", out)
		}
		if b := out[0].Blocks[0]; b.Type != BlockThinking || b.Signature != "sig-1" || b.Text != "deep thought" {
			t.Errorf("thinking block altered: %+v", b)
		}
		if b := out[0].Blocks[1]; b.Type != BlockThinking || !b.Redacted || b.Signature != "opaque" {
			t.Errorf("redacted block altered: %+v", b)
		}
	})
}

// TestConvertIDSanitization (R18): IDs sanitized to [a-zA-Z0-9_-]{1,40},
// consistently across calls and results; empty IDs become call_<n>; collisions
// are disambiguated.
func TestConvertIDSanitization(t *testing.T) {
	t.Run("bad chars and length", func(t *testing.T) {
		longID := strings.Repeat("x", 50)
		in := []Message{
			convertAssistant(convertTestModel.ID,
				toolCallBlock("id with:colons!", "a"),
				toolCallBlock(longID, "b"),
			),
			ToolResultMessage("id with:colons!", "a", false, TextBlock("r1")),
			ToolResultMessage(longID, "b", false, TextBlock("r2")),
		}
		out := normalizeMessages(convertTestModel, in)
		wantA := "id_with_colons_"
		wantB := strings.Repeat("x", 40)
		if got := out[0].Blocks[0].ID; got != wantA {
			t.Errorf("sanitized ID = %q, want %q", got, wantA)
		}
		if got := out[0].Blocks[1].ID; got != wantB {
			t.Errorf("long ID = %q (len %d), want 40 x's", got, len(got))
		}
		if out[1].ToolCallID != wantA || out[2].ToolCallID != wantB {
			t.Errorf("results not remapped consistently: %q, %q", out[1].ToolCallID, out[2].ToolCallID)
		}
	})

	t.Run("valid IDs untouched", func(t *testing.T) {
		in := []Message{
			convertAssistant(convertTestModel.ID, toolCallBlock("abc-123_XY", "t")),
			ToolResultMessage("abc-123_XY", "t", false, TextBlock("ok")),
		}
		out := normalizeMessages(convertTestModel, in)
		if out[0].Blocks[0].ID != "abc-123_XY" || out[1].ToolCallID != "abc-123_XY" {
			t.Errorf("valid ID altered: %q / %q", out[0].Blocks[0].ID, out[1].ToolCallID)
		}
	})

	t.Run("empty ID synthesized (Gemini)", func(t *testing.T) {
		in := []Message{
			convertAssistant(convertTestModel.ID, toolCallBlock("", "t")),
			ToolResultMessage("", "t", false, TextBlock("ok")),
		}
		out := normalizeMessages(convertTestModel, in)
		if got := out[0].Blocks[0].ID; got != "call_1" {
			t.Errorf("empty call ID = %q, want call_1", got)
		}
		if out[1].ToolCallID != "call_1" {
			t.Errorf("empty result ID = %q, want call_1 (consistent with call)", out[1].ToolCallID)
		}
	})

	t.Run("collisions disambiguated", func(t *testing.T) {
		// "x y" and "x:y" both sanitize to "x_y"; they must stay distinct and
		// results must follow their own call's mapping.
		in := []Message{
			convertAssistant(convertTestModel.ID,
				toolCallBlock("x y", "a"),
				toolCallBlock("x:y", "b"),
			),
			ToolResultMessage("x y", "a", false, TextBlock("r1")),
			ToolResultMessage("x:y", "b", false, TextBlock("r2")),
		}
		out := normalizeMessages(convertTestModel, in)
		id0, id1 := out[0].Blocks[0].ID, out[0].Blocks[1].ID
		if id0 == id1 {
			t.Fatalf("colliding IDs not disambiguated: both %q", id0)
		}
		for _, id := range []string{id0, id1} {
			if id == "" || len(id) > 40 || toolIDBad.MatchString(id) {
				t.Errorf("ID %q outside [a-zA-Z0-9_-]{1,40}", id)
			}
		}
		if out[1].ToolCallID != id0 || out[2].ToolCallID != id1 {
			t.Errorf("results mapped to %q/%q, want %q/%q", out[1].ToolCallID, out[2].ToolCallID, id0, id1)
		}
	})
}

// TestConvertEmptyAndVision (R19, R20).
func TestConvertEmptyAndVision(t *testing.T) {
	t.Run("empty assistant dropped", func(t *testing.T) {
		in := []Message{
			UserText("hi"),
			convertAssistant(convertTestModel.ID), // aborted before first token
			UserText("again"),
		}
		out := normalizeMessages(convertTestModel, in)
		if len(out) != 2 || out[0].Role != RoleUser || out[1].Role != RoleUser {
			t.Fatalf("out = %+v, want the empty assistant dropped", out)
		}
	})

	t.Run("empty user becomes a space", func(t *testing.T) {
		in := []Message{{Role: RoleUser, Time: time.Now()}}
		out := normalizeMessages(convertTestModel, in)
		if len(out) != 1 || len(out[0].Blocks) != 1 || out[0].Blocks[0].Text != " " {
			t.Fatalf("out = %+v, want a single-space text block", out)
		}
	})

	t.Run("blank user text becomes a space", func(t *testing.T) {
		in := []Message{UserText("")}
		out := normalizeMessages(convertTestModel, in)
		if out[0].Blocks[0].Text != " " {
			t.Fatalf("blocks = %+v", out[0].Blocks)
		}
	})

	t.Run("non-vision image downgraded", func(t *testing.T) {
		noVision := convertTestModel
		noVision.Vision = false
		in := []Message{UserBlocks(TextBlock("see"), ImageBlock("image/png", []byte{1, 2}))}
		out := normalizeMessages(noVision, in)
		b := out[0].Blocks[1]
		if b.Type != BlockText || b.Text != "[image omitted]" || b.Data != "" || b.MimeType != "" {
			t.Fatalf("image block = %+v, want text [image omitted]", b)
		}
	})

	t.Run("vision model keeps images", func(t *testing.T) {
		in := []Message{UserBlocks(ImageBlock("image/png", []byte{1, 2}))}
		out := normalizeMessages(convertTestModel, in)
		if b := out[0].Blocks[0]; b.Type != BlockImage || b.MimeType != "image/png" {
			t.Fatalf("image block altered: %+v", b)
		}
	})
}

// TestConvertInputImmutability (R5): normalizeMessages works on copies; the
// caller's slice, messages, blocks, and raw JSON are untouched even when every
// rule fires at once.
func TestConvertInputImmutability(t *testing.T) {
	noVision := convertTestModel
	noVision.Vision = false

	in := []Message{
		AppMessage("marker", json.RawMessage(`{"keep":"me"}`)),
		UserBlocks(TextBlock(""), ImageBlock("image/png", []byte{7})),
		{ // cross-model assistant with orphaned bad-ID calls and thinking
			Role: RoleAssistant, Model: "other-model", Time: time.Now(),
			Blocks: []Block{
				{Type: BlockThinking, Text: "think", Signature: "sig"},
				{Type: BlockThinking, Redacted: true, Signature: "r"},
				{Type: BlockToolCall, ID: "bad id!", Name: "t", Args: json.RawMessage(`{"a":1}`)},
				{Type: BlockToolCall, ID: "", Name: "u", Args: json.RawMessage(`{}`)},
			},
		},
		ToolResultMessage("bad id!", "t", false, TextBlock("res")),
		{Role: RoleAssistant, Model: "other-model", Time: time.Now()}, // empty: dropped
	}

	before, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := normalizeMessages(noVision, in)
	after, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("input mutated by normalizeMessages:\nbefore: %s\nafter:  %s", before, after)
	}

	// And the output really did apply the rules (sanity that the copy path is
	// the one that changed).
	for _, m := range out {
		if m.Kind != "" {
			t.Error("Kind message present in output")
		}
		for _, b := range m.Blocks {
			if b.Type == BlockImage {
				t.Error("image survived non-vision normalization")
			}
			if b.Type == BlockThinking {
				t.Error("cross-model thinking block survived handoff conversion")
			}
		}
	}
}

// TestConvertDocumentDowngrade (R38): a model that does not take documents is
// told one was left out rather than handed bytes it cannot read, and one that
// does keeps them untouched.
func TestConvertDocumentDowngrade(t *testing.T) {
	withDocs := convertTestModel
	withDocs.Documents = true

	t.Run("model without documents sees text", func(t *testing.T) {
		in := []Message{UserBlocks(TextBlock("read this"), DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2}))}
		out := normalizeMessages(convertTestModel, in)
		b := out[0].Blocks[1]
		if b.Type != BlockText || b.Text != "[document spec.pdf omitted]" || b.Data != "" || b.MimeType != "" {
			t.Fatalf("document block = %+v, want text naming the file", b)
		}
	})

	t.Run("an unnamed document still says one was left out", func(t *testing.T) {
		in := []Message{UserBlocks(DocumentBlock("application/pdf", "", []byte{1}))}
		out := normalizeMessages(convertTestModel, in)
		if b := out[0].Blocks[0]; b.Text != "[document omitted]" {
			t.Fatalf("block = %+v", b)
		}
	})

	t.Run("model with documents keeps them", func(t *testing.T) {
		in := []Message{UserBlocks(DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2}))}
		out := normalizeMessages(withDocs, in)
		b := out[0].Blocks[0]
		if b.Type != BlockDocument || b.MimeType != "application/pdf" || b.Name != "spec.pdf" {
			t.Fatalf("document block altered: %+v", b)
		}
	})

	t.Run("the caller's blocks are not mutated", func(t *testing.T) {
		in := []Message{UserBlocks(DocumentBlock("application/pdf", "spec.pdf", []byte{1, 2}))}
		before, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		normalizeMessages(convertTestModel, in)
		after, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("input mutated:\nbefore: %s\nafter:  %s", before, after)
		}
	})
}

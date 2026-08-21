package llm

import (
	"encoding/json"
	"testing"
)

// TestCoreSmoke is a fast sanity check of the stream accumulator written
// alongside the core; the full suites live in the dedicated test files.
func TestCoreSmoke(t *testing.T) {
	s := NewStream(ClaudeSonnet45, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "Hello "})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "world"})
		emit(Event{Type: EventTextEnd, Index: 0})
		emit(Event{Type: EventToolCallStart, Index: 1, Block: &Block{Type: BlockToolCall, ID: "t1", Name: "read_file"}})
		emit(Event{Type: EventToolCallDelta, Index: 1, Delta: `{"path":"ma`})
		emit(Event{Type: EventToolCallDelta, Index: 1, Delta: `in.go"}`})
		emit(Event{Type: EventToolCallEnd, Index: 1})
		emit(DoneEvent(StopToolUse, Usage{Input: 100, Output: 20}))
	})

	var sawPartialArgs bool
	for ev := range s.Events() {
		if ev.Type == EventToolCallDelta {
			var m map[string]any
			if err := json.Unmarshal(ev.Message.Blocks[1].Args, &m); err != nil {
				t.Fatalf("partial args not valid JSON: %v (%s)", err, ev.Message.Blocks[1].Args)
			}
			sawPartialArgs = true
		}
	}
	if !sawPartialArgs {
		t.Fatal("no tool_call_delta observed")
	}

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want tool_use", msg.StopReason)
	}
	if got := textOf(msg); got != "Hello world" {
		t.Fatalf("text = %q", got)
	}
	if msg.Blocks[1].ID != "t1" || msg.Blocks[1].Name != "read_file" {
		t.Fatalf("tool block = %+v", msg.Blocks[1])
	}
	var args map[string]string
	if err := json.Unmarshal(msg.Blocks[1].Args, &args); err != nil || args["path"] != "main.go" {
		t.Fatalf("args = %s (err %v)", msg.Blocks[1].Args, err)
	}
	if msg.Usage == nil || msg.Usage.Input != 100 || msg.Usage.TotalCost <= 0 {
		t.Fatalf("usage = %+v", msg.Usage)
	}

	// Second range yields nothing (R9); Message stays stable.
	for range s.Events() {
		t.Fatal("second Events() range must be empty")
	}
	again, _ := s.Message()
	if textOf(again) != "Hello world" {
		t.Fatal("Message not idempotent")
	}
}

package llm

// Tests for the NewStream accumulator (SPEC R6, R7, R9, R10).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// streamTestModel has a distinctive price table so cost math is checkable.
var streamTestModel = Model{
	ID: "stream-test-model", API: AnthropicMessages, Provider: "testprov",
	Cost: Cost{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.25},
}

// streamSnapshot deep-copies a live Event.Message for later assertions.
func streamSnapshot(m *Message) Message {
	c := *m
	c.Blocks = make([]Block, len(m.Blocks))
	copy(c.Blocks, m.Blocks)
	for i := range c.Blocks {
		if len(c.Blocks[i].Args) > 0 {
			c.Blocks[i].Args = append(json.RawMessage(nil), c.Blocks[i].Args...)
		}
	}
	if m.Usage != nil {
		u := *m.Usage
		c.Usage = &u
	}
	return c
}

// TestStreamAccumulatorInterleaved streams a text block (index 0) and a tool
// call (index 1) with alternating deltas (R6: different blocks may interleave)
// and checks the partial Message at every step plus the final result.
func TestStreamAccumulatorInterleaved(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventToolCallStart, Index: 1, Block: &Block{Type: BlockToolCall, ID: "tc1", Name: "lookup"}})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "Hel"})
		emit(Event{Type: EventToolCallDelta, Index: 1, Delta: `{"q":"go`})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "lo"})
		emit(Event{Type: EventToolCallDelta, Index: 1, Delta: `pher"}`})
		emit(Event{Type: EventTextEnd, Index: 0})
		emit(Event{Type: EventToolCallEnd, Index: 1})
		emit(DoneEvent(StopToolUse, Usage{Input: 10, Output: 5}))
	})

	var (
		livePtr *Message
		snaps   []Message
		types   []EventType
		blocks  []*Block
	)
	for ev := range s.Events() {
		if livePtr == nil {
			livePtr = ev.Message
		} else if ev.Message != livePtr {
			t.Fatal("Event.Message must be the same live accumulator on every event")
		}
		snaps = append(snaps, streamSnapshot(ev.Message))
		types = append(types, ev.Type)
		blocks = append(blocks, ev.Block)
	}

	wantTypes := []EventType{
		EventStart,
		EventTextStart, EventToolCallStart,
		EventTextDelta, EventToolCallDelta,
		EventTextDelta, EventToolCallDelta,
		EventTextEnd, EventToolCallEnd,
		EventDone,
	}
	if fmt.Sprint(types) != fmt.Sprint(wantTypes) {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}

	// Partial state after each step.
	if got := snaps[3].Blocks[0].Text; got != "Hel" {
		t.Errorf("after first text delta: text = %q, want %q", got, "Hel")
	}
	if got := string(snaps[3].Blocks[1].Args); got != "{}" {
		t.Errorf("tool args before any delta = %s, want {}", got)
	}
	if got := string(snaps[4].Blocks[1].Args); got != `{"q":"go"}` {
		t.Errorf("partial tool args = %s, want %s (auto-closed prefix, R7)", got, `{"q":"go"}`)
	}
	if got := snaps[5].Blocks[0].Text; got != "Hello" {
		t.Errorf("after second text delta: text = %q, want %q", got, "Hello")
	}
	if got := string(snaps[6].Blocks[1].Args); got != `{"q":"gopher"}` {
		t.Errorf("complete tool args = %s", got)
	}
	for i, s := range snaps {
		if len(s.Blocks) > 1 && len(s.Blocks[1].Args) > 0 && !json.Valid(s.Blocks[1].Args) {
			t.Errorf("event %d: partial Args %s not valid JSON", i, s.Blocks[1].Args)
		}
	}

	// *_end events carry the completed block.
	if b := blocks[7]; b == nil || b.Type != BlockText || b.Text != "Hello" {
		t.Errorf("text_end Block = %+v", blocks[7])
	}
	if b := blocks[8]; b == nil || b.ID != "tc1" || b.Name != "lookup" || string(b.Args) != `{"q":"gopher"}` {
		t.Errorf("tool_call_end Block = %+v", blocks[8])
	}

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if msg.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", msg.StopReason)
	}
	if msg.Model != streamTestModel.ID || msg.Provider != "testprov" || msg.API != AnthropicMessages || msg.Role != RoleAssistant {
		t.Errorf("message metadata = role %q model %q provider %q api %q", msg.Role, msg.Model, msg.Provider, msg.API)
	}
	if textOf(msg) != "Hello" {
		t.Errorf("final text = %q", textOf(msg))
	}
	if msg.Usage == nil || msg.Usage.Input != 10 || msg.Usage.Output != 5 {
		t.Fatalf("usage = %+v", msg.Usage)
	}
	if want := ComputeCost(streamTestModel, *msg.Usage); msg.Usage.TotalCost != want {
		t.Errorf("TotalCost = %v, want %v", msg.Usage.TotalCost, want)
	}

	// Event.Message is live: the pointer captured mid-stream now reflects the
	// final state, while the early snapshot kept its partial content.
	if livePtr.Blocks[0].Text != "Hello" || livePtr.StopReason != StopToolUse {
		t.Error("live Event.Message did not accumulate to the final state")
	}
	if snaps[3].Blocks[0].Text != "Hel" {
		t.Error("snapshot mutated: copies must be insulated from the live accumulator")
	}
}

// TestStreamAccumulatorSingleTerminal verifies exactly one terminal event
// reaches the consumer and that raw events emitted after the terminal are
// ignored (emit returns false, content unchanged).
func TestStreamAccumulatorSingleTerminal(t *testing.T) {
	var afterTerminal []bool
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "ok"})
		emit(Event{Type: EventTextEnd, Index: 0})
		emit(DoneEvent(StopEnd, Usage{Output: 1}))
		// Everything below must be ignored.
		afterTerminal = append(afterTerminal, emit(Event{Type: EventTextDelta, Index: 0, Delta: " IGNORED"}))
		afterTerminal = append(afterTerminal, emit(DoneEvent(StopLength, Usage{Output: 99})))
		afterTerminal = append(afterTerminal, emit(ErrorEvent(errors.New("late error"), Usage{})))
	})

	events := collectEvents(s)
	terminals := 0
	for _, ev := range events {
		if ev.Type == EventDone || ev.Type == EventError {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("consumer saw %d terminal events, want exactly 1 (types %v)", terminals, eventTypes(events))
	}
	if last := events[len(events)-1]; last.Type != EventDone {
		t.Fatalf("last event = %s, want done", last.Type)
	}
	for i, ok := range afterTerminal {
		if ok {
			t.Errorf("emit #%d after terminal returned true, want false", i)
		}
	}

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if textOf(msg) != "ok" {
		t.Errorf("text = %q: content emitted after the terminal event must be dropped", textOf(msg))
	}
	if msg.StopReason != StopEnd || msg.Usage == nil || msg.Usage.Output != 1 {
		t.Errorf("stop = %q usage = %+v: terminal state must not be overwritten", msg.StopReason, msg.Usage)
	}
}

// TestStreamAccumulatorNoTerminalMessage: a producer that returns without a
// terminal event yields a synthesized error on Message() (R10), with partial
// content preserved.
func TestStreamAccumulatorNoTerminalMessage(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "hi"})
		// returns without Done/Error
	})
	msg, err := s.Message()
	if err == nil || !contains(err.Error(), "ended without a terminal event") {
		t.Fatalf("err = %v, want 'ended without a terminal event'", err)
	}
	if msg.StopReason != StopError {
		t.Errorf("stop = %q, want error", msg.StopReason)
	}
	if msg.ErrorText == "" {
		t.Error("ErrorText not set")
	}
	if textOf(msg) != "hi" {
		t.Errorf("partial text = %q, want %q", textOf(msg), "hi")
	}
}

// TestStreamAccumulatorNoTerminalEventDelivered: per R6/R10 the *event stream*
// must contain exactly one terminal event, synthesized as an EventError when
// the producer returns without one.
func TestStreamAccumulatorNoTerminalEventDelivered(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "hi"})
	})
	events := collectEvents(s)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("last event = %s, want error (R6: exactly one terminal event)", last.Type)
	}
	if last.Err == nil || !contains(last.Err.Error(), "ended without a terminal event") {
		t.Fatalf("terminal Err = %v", last.Err)
	}
}

// TestStreamAccumulatorPanic: a panicking producer becomes an EventError with
// the partial content preserved (R10).
func TestStreamAccumulatorPanic(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "partial "})
		panic("boom")
	})
	events := collectEvents(s)
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("last event = %s, want error", last.Type)
	}
	if last.Err == nil || !contains(last.Err.Error(), "panic") || !contains(last.Err.Error(), "boom") {
		t.Fatalf("Err = %v, want panic message with payload", last.Err)
	}

	msg, err := s.Message()
	if err == nil || !contains(err.Error(), "panic") {
		t.Fatalf("Message err = %v", err)
	}
	if msg.StopReason != StopError || !contains(msg.ErrorText, "panic") {
		t.Errorf("stop = %q errorText = %q", msg.StopReason, msg.ErrorText)
	}
	if textOf(msg) != "partial " {
		t.Errorf("partial content = %q, want %q", textOf(msg), "partial ")
	}
}

// TestStreamAccumulatorBlockOverride: a non-nil Block on *_end events merges
// into the accumulated block — non-zero override fields win, accumulated Text
// is kept when the override's Text is empty.
func TestStreamAccumulatorBlockOverride(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		// Thinking: signature attached at end, accumulated text kept.
		emit(Event{Type: EventThinkingStart, Index: 0})
		emit(Event{Type: EventThinkingDelta, Index: 0, Delta: "mull"})
		emit(Event{Type: EventThinkingEnd, Index: 0, Block: &Block{Type: BlockThinking, Signature: "sig-abc"}})
		// Text: non-empty override Text replaces the accumulated text.
		emit(Event{Type: EventTextStart, Index: 1})
		emit(Event{Type: EventTextDelta, Index: 1, Delta: "draft"})
		emit(Event{Type: EventTextEnd, Index: 1, Block: &Block{Type: BlockText, Text: "authoritative"}})
		// Tool call: ID/Name/Args attached authoritatively at end.
		emit(Event{Type: EventToolCallStart, Index: 2})
		emit(Event{Type: EventToolCallDelta, Index: 2, Delta: `{"x":`})
		emit(Event{Type: EventToolCallEnd, Index: 2, Block: &Block{
			Type: BlockToolCall, ID: "id9", Name: "nm", Args: json.RawMessage(`{"x":1}`),
		}})
		emit(DoneEvent(StopEnd, Usage{}))
	})

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if len(msg.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(msg.Blocks))
	}
	if b := msg.Blocks[0]; b.Type != BlockThinking || b.Text != "mull" || b.Signature != "sig-abc" {
		t.Errorf("thinking block = %+v: want accumulated text kept, signature merged", b)
	}
	if b := msg.Blocks[1]; b.Type != BlockText || b.Text != "authoritative" {
		t.Errorf("text block = %+v: want override text to win", b)
	}
	if b := msg.Blocks[2]; b.ID != "id9" || b.Name != "nm" || string(b.Args) != `{"x":1}` {
		t.Errorf("tool block = %+v: want ID/Name/Args from override", b)
	}
}

// TestStreamAccumulatorBreakEarly: breaking the Events() range aborts the
// stream — StopAborted, finalErr wraps context.Canceled, partials kept (R9).
func TestStreamAccumulatorBreakEarly(t *testing.T) {
	var emitAfterBreak []bool
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "a"})
		emitAfterBreak = append(emitAfterBreak, emit(Event{Type: EventTextDelta, Index: 0, Delta: "b"}))
		emitAfterBreak = append(emitAfterBreak, emit(DoneEvent(StopEnd, Usage{})))
	})

	for ev := range s.Events() {
		if ev.Type == EventTextDelta {
			break // consumer stops after the first delta
		}
	}
	for i, ok := range emitAfterBreak {
		if ok {
			t.Errorf("emit #%d after consumer break returned true, want false", i)
		}
	}

	msg, err := s.Message()
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want wrapped context.Canceled", err)
	}
	if msg.StopReason != StopAborted {
		t.Errorf("stop = %q, want aborted", msg.StopReason)
	}
	if textOf(msg) != "a" {
		t.Errorf("partial text = %q, want %q", textOf(msg), "a")
	}
}

// TestStreamAccumulatorMessageFirst: Message() before any Events() range runs
// the producer to completion; a subsequent range yields nothing and Message()
// is idempotent (R9).
func TestStreamAccumulatorMessageFirst(t *testing.T) {
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(Event{Type: EventTextStart, Index: 0})
		emit(Event{Type: EventTextDelta, Index: 0, Delta: "hello"})
		emit(Event{Type: EventTextEnd, Index: 0})
		emit(DoneEvent(StopEnd, Usage{Output: 2}))
	})

	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if textOf(msg) != "hello" || msg.StopReason != StopEnd {
		t.Fatalf("msg = %+v", msg)
	}

	for range s.Events() {
		t.Fatal("Events() after Message() must yield nothing")
	}

	again, err2 := s.Message()
	if err2 != nil || textOf(again) != "hello" || again.StopReason != StopEnd {
		t.Fatalf("Message() not idempotent: %+v, %v", again, err2)
	}
}

// TestStreamAccumulatorTerminalStops covers stop-reason derivation on
// terminal events.
func TestStreamAccumulatorTerminalStops(t *testing.T) {
	run := func(terminal Event) (Message, error) {
		s := NewStream(streamTestModel, func(emit func(Event) bool) {
			emit(Event{Type: EventStart})
			emit(terminal)
		})
		return s.Message()
	}

	t.Run("done with empty stop defaults to StopEnd", func(t *testing.T) {
		msg, err := run(DoneEvent("", Usage{}))
		if err != nil || msg.StopReason != StopEnd {
			t.Fatalf("stop = %q err = %v, want stop/nil", msg.StopReason, err)
		}
	})
	t.Run("error with context.Canceled is StopAborted", func(t *testing.T) {
		cause := fmt.Errorf("request aborted: %w", context.Canceled)
		msg, err := run(ErrorEvent(cause, Usage{}))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if msg.StopReason != StopAborted {
			t.Errorf("stop = %q, want aborted", msg.StopReason)
		}
		if msg.ErrorText == "" || !contains(msg.ErrorText, "request aborted") {
			t.Errorf("ErrorText = %q", msg.ErrorText)
		}
	})
	t.Run("error with context.DeadlineExceeded is StopAborted", func(t *testing.T) {
		msg, err := run(ErrorEvent(context.DeadlineExceeded, Usage{}))
		if err == nil || msg.StopReason != StopAborted {
			t.Fatalf("stop = %q err = %v", msg.StopReason, err)
		}
	})
	t.Run("other error is StopError with ErrorText", func(t *testing.T) {
		msg, err := run(ErrorEvent(errors.New("provider melted"), Usage{}))
		if err == nil || err.Error() != "provider melted" {
			t.Fatalf("err = %v", err)
		}
		if msg.StopReason != StopError || msg.ErrorText != "provider melted" {
			t.Errorf("stop = %q errorText = %q", msg.StopReason, msg.ErrorText)
		}
	})
}

// TestStreamAccumulatorCost: the accumulator computes Usage.TotalCost from the
// model's price table on the final usage (R4), overriding whatever the
// producer put in the usage it passed.
func TestStreamAccumulatorCost(t *testing.T) {
	usage := Usage{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100, TotalCost: 999} // bogus TotalCost
	s := NewStream(streamTestModel, func(emit func(Event) bool) {
		emit(Event{Type: EventStart})
		emit(DoneEvent(StopEnd, usage))
	})
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	want := ComputeCost(streamTestModel, usage)
	const wantLiteral = (1000*1.0 + 500*2.0 + 200*0.5 + 100*0.25) / 1e6
	if want != wantLiteral {
		t.Fatalf("ComputeCost = %v, want %v", want, wantLiteral)
	}
	if msg.Usage == nil || msg.Usage.TotalCost != want {
		t.Fatalf("TotalCost = %+v, want %v (producer-supplied TotalCost must be recomputed)", msg.Usage, want)
	}
}

// contains aliases strings.Contains for terse assertions in this file.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

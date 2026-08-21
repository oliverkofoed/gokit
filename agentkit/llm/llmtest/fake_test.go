package llmtest

// Tests for the scripted fake (SPEC §9, R26, R27). The fake builds its
// streams through llm.NewStream, so these also exercise the real accumulator.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

var fakeModel = llm.Model{
	ID: "fake-model", API: llm.OpenAIChat, Provider: "faketest",
	Cost: llm.Cost{Input: 1, Output: 1},
}

func fakeRequest(msgs ...llm.Message) llm.Request {
	return llm.Request{Model: fakeModel, System: "sys!", Messages: msgs}
}

// fakeCollect drains a stream, snapshotting the live Message on each event.
func fakeCollect(s *llm.Stream) []llm.Event {
	var out []llm.Event
	for ev := range s.Events() {
		if ev.Message != nil {
			m := *ev.Message
			m.Blocks = append([]llm.Block(nil), m.Blocks...)
			for i := range m.Blocks {
				if len(m.Blocks[i].Args) > 0 {
					m.Blocks[i].Args = append(json.RawMessage(nil), m.Blocks[i].Args...)
				}
			}
			ev.Message = &m
		}
		out = append(out, ev)
	}
	return out
}

func fakeTypes(events []llm.Event) []llm.EventType {
	out := make([]llm.EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

// TestFakeText: text replies stream as word-level deltas through the real
// accumulator, with usage estimated at len/4.
func TestFakeText(t *testing.T) {
	f := New(Text("alpha beta gamma"))
	req := fakeRequest(llm.UserText("hello there"))
	events := fakeCollect(f.Stream(context.Background(), req))

	want := []llm.EventType{
		llm.EventStart, llm.EventTextStart,
		llm.EventTextDelta, llm.EventTextDelta, llm.EventTextDelta,
		llm.EventTextEnd, llm.EventDone,
	}
	if fmt.Sprint(fakeTypes(events)) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", fakeTypes(events), want)
	}
	var deltas []string
	for _, ev := range events {
		if ev.Type == llm.EventTextDelta {
			deltas = append(deltas, ev.Delta)
		}
	}
	if fmt.Sprint(deltas) != fmt.Sprint([]string{"alpha ", "beta ", "gamma"}) {
		t.Fatalf("deltas = %q", deltas)
	}
	// Partial message grows delta by delta.
	if got := events[2].Message.Blocks[0].Text; got != "alpha " {
		t.Errorf("partial after first delta = %q", got)
	}

	final := events[len(events)-1].Message
	if final.StopReason != llm.StopEnd {
		t.Errorf("stop = %q, want stop (inferred)", final.StopReason)
	}
	if final.Blocks[0].Text != "alpha beta gamma" {
		t.Errorf("final text = %q", final.Blocks[0].Text)
	}
	if final.Usage == nil {
		t.Fatal("no usage")
	}
	if wantOut := len("alpha beta gamma") / 4; final.Usage.Output != wantOut {
		t.Errorf("Output = %d, want %d (len/4)", final.Usage.Output, wantOut)
	}
	if wantIn := (len("sys!") + len("hello there")) / 4; final.Usage.Input != wantIn {
		t.Errorf("Input = %d, want %d", final.Usage.Input, wantIn)
	}
	if final.Model != fakeModel.ID || final.Provider != fakeModel.Provider {
		t.Errorf("attribution = %q/%q", final.Model, final.Provider)
	}
}

// TestFakeToolCall: args stream as two JSON fragments, the mid-stream partial
// parse is valid JSON (R7 via the real accumulator), and the stop reason is
// inferred as tool_use.
func TestFakeToolCall(t *testing.T) {
	f := New(ToolCall("search", map[string]string{"query": "golang iterators"}))
	events := fakeCollect(f.Stream(context.Background(), fakeRequest(llm.UserText("find it"))))

	want := []llm.EventType{
		llm.EventStart, llm.EventToolCallStart,
		llm.EventToolCallDelta, llm.EventToolCallDelta,
		llm.EventToolCallEnd, llm.EventDone,
	}
	if fmt.Sprint(fakeTypes(events)) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", fakeTypes(events), want)
	}

	// Mid-stream: after the first fragment the partial parse must already be
	// valid JSON, and must not yet equal the full arguments.
	mid := events[2].Message.Blocks[0].Args
	if !json.Valid(mid) {
		t.Fatalf("mid-stream Args = %s: not valid JSON", mid)
	}
	full := `{"query":"golang iterators"}`
	if string(mid) == full {
		t.Errorf("mid-stream Args already complete; want a partial parse")
	}

	final := events[len(events)-1].Message
	if final.StopReason != llm.StopToolUse {
		t.Errorf("stop = %q, want tool_use (inferred from tool_call block)", final.StopReason)
	}
	b := final.Blocks[0]
	if b.Type != llm.BlockToolCall || b.Name != "search" || string(b.Args) != full {
		t.Errorf("tool block = %+v", b)
	}
	if b.ID != "call_1" {
		t.Errorf("ID = %q, want call_1", b.ID)
	}

	// A second tool call on the same fake gets the next generated ID.
	f.Append(ToolCall("search", map[string]string{"query": "again"}))
	msg, err := f.Stream(context.Background(), fakeRequest()).Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Blocks[0].ID != "call_2" {
		t.Errorf("second ID = %q, want call_2", msg.Blocks[0].ID)
	}
}

// TestFakeQueueExhaustion: an empty queue is a loud EventError, not a hang.
func TestFakeQueueExhaustion(t *testing.T) {
	f := New()
	events := fakeCollect(f.Stream(context.Background(), fakeRequest()))
	if len(events) != 2 || events[0].Type != llm.EventStart || events[1].Type != llm.EventError {
		t.Fatalf("events = %v, want [start error]", fakeTypes(events))
	}
	msg := events[1].Message
	if msg.StopReason != llm.StopError {
		t.Errorf("stop = %q", msg.StopReason)
	}
	if events[1].Err == nil || !errContains(events[1].Err, "no replies queued") {
		t.Errorf("err = %v, want 'no replies queued'", events[1].Err)
	}
}

// TestFakeAppendMidRun: Append during an in-flight stream feeds the next call.
func TestFakeAppendMidRun(t *testing.T) {
	f := New(Text("one"))
	s := f.Stream(context.Background(), fakeRequest())
	appended := false
	for ev := range s.Events() {
		if !appended && ev.Type == llm.EventTextDelta {
			f.Append(Text("two")) // mid-run, from the consumer side
			appended = true
		}
	}
	msg1, err := s.Message()
	if err != nil || msg1.Blocks[0].Text != "one" {
		t.Fatalf("first = %+v, %v", msg1, err)
	}
	msg2, err := f.Stream(context.Background(), fakeRequest()).Message()
	if err != nil || msg2.Blocks[0].Text != "two" {
		t.Fatalf("second = %+v, %v", msg2, err)
	}
}

// TestFakeRequests: Requests() returns copies capturing what each call saw.
func TestFakeRequests(t *testing.T) {
	f := New(Text("a"), Text("b"))
	msgs := []llm.Message{llm.UserText("original")}
	req := fakeRequest(msgs...)
	req.Tools = []llm.ToolDef{{Name: "grep", Description: "d", Schema: json.RawMessage(`{}`)}}
	if _, err := f.Stream(context.Background(), req).Message(); err != nil {
		t.Fatal(err)
	}

	// Mutate the caller's slices after the call; the capture must not change.
	msgs[0] = llm.UserText("mutated")
	req.Tools[0] = llm.ToolDef{Name: "changed"}

	if _, err := f.Stream(context.Background(), fakeRequest(llm.UserText("second"))).Message(); err != nil {
		t.Fatal(err)
	}

	reqs := f.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Requests() = %d, want 2", len(reqs))
	}
	if got := reqs[0].Messages[0].Blocks[0].Text; got != "original" {
		t.Errorf("captured message = %q, want the value at call time", got)
	}
	if got := reqs[0].Tools[0].Name; got != "grep" {
		t.Errorf("captured tool = %q, want grep", got)
	}
	if reqs[0].System != "sys!" || reqs[0].Model.ID != fakeModel.ID {
		t.Errorf("captured system/model = %q/%q", reqs[0].System, reqs[0].Model.ID)
	}
	if got := reqs[1].Messages[0].Blocks[0].Text; got != "second" {
		t.Errorf("second capture = %q", got)
	}

	// The returned slice is itself a copy.
	reqs[0] = llm.Request{}
	if f.Requests()[0].System != "sys!" {
		t.Error("mutating the returned slice affected the fake's internal state")
	}
}

// TestFakeBlockerCtxCancel (R27): a Blocker holds the stream open before its
// terminal event; cancelling the context during the wait aborts with partial
// content — all deterministically, no sleeps.
func TestFakeBlockerCtxCancel(t *testing.T) {
	blocker := make(chan struct{}) // never released
	f := New(Reply{Blocks: []llm.Block{llm.TextBlock("partial text")}, Blocker: blocker})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := f.Stream(ctx, fakeRequest())

	var sawEnd, sawError bool
	for ev := range s.Events() {
		switch ev.Type {
		case llm.EventTextEnd:
			sawEnd = true
			cancel() // produce will next wait on the Blocker; ctx wins
		case llm.EventDone:
			t.Fatal("stream completed despite unreleased Blocker")
		case llm.EventError:
			sawError = true
		}
	}
	if !sawEnd || !sawError {
		t.Fatalf("sawEnd=%v sawError=%v", sawEnd, sawError)
	}

	msg, err := s.Message()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if msg.StopReason != llm.StopAborted {
		t.Errorf("stop = %q, want aborted", msg.StopReason)
	}
	if msg.Blocks[0].Text != "partial text" {
		t.Errorf("partial content = %q", msg.Blocks[0].Text)
	}
}

// TestFakeBlockerRelease: releasing the Blocker lets the terminal event
// through; content events all arrive before the release.
func TestFakeBlockerRelease(t *testing.T) {
	blocker := make(chan struct{})
	f := New(Reply{Blocks: []llm.Block{llm.TextBlock("held")}, Blocker: blocker})

	s := f.Stream(context.Background(), fakeRequest())
	var types []llm.EventType
	for ev := range s.Events() {
		types = append(types, ev.Type)
		if ev.Type == llm.EventTextEnd {
			close(blocker) // only now may the terminal arrive
		}
	}
	if types[len(types)-1] != llm.EventDone {
		t.Fatalf("events = %v, want done last", types)
	}
	msg, err := s.Message()
	if err != nil || msg.StopReason != llm.StopEnd || msg.Blocks[0].Text != "held" {
		t.Fatalf("msg = %+v, %v", msg, err)
	}
}

// TestFakeStopReasons: inference ("" → tool_use with a tool_call block, else
// stop) and explicit override.
func TestFakeStopReasons(t *testing.T) {
	t.Run("text infers stop", func(t *testing.T) {
		msg, err := New(Text("x")).Stream(context.Background(), fakeRequest()).Message()
		if err != nil || msg.StopReason != llm.StopEnd {
			t.Fatalf("stop = %q, err %v", msg.StopReason, err)
		}
	})
	t.Run("mixed blocks with tool call infer tool_use", func(t *testing.T) {
		f := New(Reply{Blocks: []llm.Block{
			llm.TextBlock("let me check"),
			{Type: llm.BlockToolCall, Name: "look", Args: json.RawMessage(`{"a":1}`)},
		}})
		msg, err := f.Stream(context.Background(), fakeRequest()).Message()
		if err != nil || msg.StopReason != llm.StopToolUse {
			t.Fatalf("stop = %q, err %v", msg.StopReason, err)
		}
		if len(msg.Blocks) != 2 || msg.Blocks[0].Text != "let me check" || msg.Blocks[1].Name != "look" {
			t.Fatalf("blocks = %+v", msg.Blocks)
		}
	})
	t.Run("explicit StopReason wins", func(t *testing.T) {
		f := New(Blocks(llm.StopLength, llm.TextBlock("truncat")))
		msg, err := f.Stream(context.Background(), fakeRequest()).Message()
		if err != nil || msg.StopReason != llm.StopLength {
			t.Fatalf("stop = %q, err %v", msg.StopReason, err)
		}
	})
	t.Run("thinking blocks stream as thinking events", func(t *testing.T) {
		f := New(Blocks(llm.StopEnd, llm.Block{Type: llm.BlockThinking, Text: "hmm hm"}, llm.TextBlock("ok")))
		events := fakeCollect(f.Stream(context.Background(), fakeRequest()))
		var saw []llm.EventType
		for _, ev := range events {
			if ev.Type == llm.EventThinkingStart || ev.Type == llm.EventThinkingDelta || ev.Type == llm.EventThinkingEnd {
				saw = append(saw, ev.Type)
			}
		}
		if len(saw) < 3 {
			t.Fatalf("thinking events = %v", saw)
		}
		final := events[len(events)-1].Message
		if final.Blocks[0].Type != llm.BlockThinking || final.Blocks[0].Text != "hmm hm" {
			t.Fatalf("blocks = %+v", final.Blocks)
		}
	})
}

// TestFakeErrorReply: Error replies end the stream with EventError.
func TestFakeErrorReply(t *testing.T) {
	sentinel := errors.New("model on fire")
	f := New(Error(sentinel))
	s := f.Stream(context.Background(), fakeRequest())
	events := fakeCollect(s)
	if events[len(events)-1].Type != llm.EventError {
		t.Fatalf("events = %v", fakeTypes(events))
	}
	msg, err := s.Message()
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if msg.StopReason != llm.StopError || msg.ErrorText != "model on fire" {
		t.Fatalf("stop = %q, errorText = %q", msg.StopReason, msg.ErrorText)
	}
}

func errContains(err error, sub string) bool {
	return err != nil && strings.Contains(err.Error(), sub)
}

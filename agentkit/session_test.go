package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm"
	"github.com/oliverkofoed/gokit/agentkit/llm/llmtest"
)

// ---- helpers -----------------------------------------------------------------

// stCollect drains a run, snapshotting per-event pointers.
func stCollect(events func(func(Event) bool)) []Event {
	var out []Event
	for ev := range events {
		if ev.Message != nil {
			m := *ev.Message
			ev.Message = &m
		}
		out = append(out, ev)
	}
	return out
}

func stTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func stRunEnd(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].Type != EventRunEnd {
		t.Fatalf("last event is not run_end: %v", stTypes(events))
	}
	return events[len(events)-1]
}

// stNoop is a tool that trivially succeeds.
func stNoop(name string) Tool {
	return Tool{
		Name: name, Description: "noop", Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			return Text("done"), nil
		},
	}
}

func stRoles(msgs []llm.Message) []llm.Role {
	out := make([]llm.Role, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

// ---- the §12.6 matrix ----------------------------------------------------------

func TestSingleTurn(t *testing.T) {
	fake := llmtest.New(llmtest.Text("hi there"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})

	events := stCollect(s.Run(context.Background(), "hello"))

	types := stTypes(events)
	if types[0] != EventRunStart || types[1] != EventMessage || types[2] != EventTurnStart {
		t.Fatalf("prefix = %v", types[:3])
	}
	if events[1].Message.Role != llm.RoleUser {
		t.Fatalf("first message not user: %+v", events[1].Message)
	}
	n := len(types)
	if types[n-3] != EventMessage || types[n-2] != EventTurnEnd || types[n-1] != EventRunEnd {
		t.Fatalf("suffix = %v", types[n-3:])
	}
	if events[n-3].Message.Role != llm.RoleAssistant {
		t.Fatal("penultimate message not assistant")
	}
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatalf("run_end err = %v", err)
	}
	// Model deltas are sandwiched between turn_start and message(assistant),
	// and every event in the turn carries Turn == 1.
	sawDelta := false
	for _, ev := range events {
		if ev.Type == EventModel && ev.Stream.Type == llm.EventTextDelta {
			sawDelta = true
		}
		if ev.Type == EventTurnStart && ev.Turn != 1 {
			t.Fatalf("turn_start Turn = %d", ev.Turn)
		}
	}
	if !sawDelta {
		t.Fatal("no text deltas forwarded")
	}
}

func TestToolLoop(t *testing.T) {
	fake := llmtest.New(
		llmtest.ToolCall("noop", map[string]any{}),
		llmtest.Text("finished"),
	)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}})
	events := stCollect(s.Run(context.Background(), "go"))
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}

	reqs := fake.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleToolResult}
	if !reflect.DeepEqual(stRoles(reqs[1].Messages), want) {
		t.Fatalf("second request roles = %v", stRoles(reqs[1].Messages))
	}
}

func TestParallelBatchOrdering(t *testing.T) {
	// Three tools complete in order 3, 1, 2; results must still be appended
	// in assistant block order 1, 2, 3 (R33). Channel choreography, no sleeps.
	entered := make(chan int, 3)
	ended := make(chan string, 3) // fed from observed tool_end events below
	gates := map[string]chan struct{}{
		"t1": make(chan struct{}), "t2": make(chan struct{}), "t3": make(chan struct{}),
	}
	mkTool := func(name string, id int) Tool {
		return Tool{
			Name: name, Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
			Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
				entered <- id
				<-gates[name]
				return Text("%s done", name), nil
			},
		}
	}

	// Release gates one at a time, each only after the previous tool's
	// tool_end EVENT was observed — so the asserted event order is forced,
	// not raced.
	go func() {
		for range 3 {
			<-entered // all three running concurrently
		}
		close(gates["t3"])
		for <-ended != "t3" {
		}
		close(gates["t1"])
		for <-ended != "t1" {
		}
		close(gates["t2"])
	}()

	fake := llmtest.New(
		llmtest.Blocks(llm.StopToolUse,
			llm.Block{Type: llm.BlockToolCall, ID: "c1", Name: "t1", Args: json.RawMessage(`{}`)},
			llm.Block{Type: llm.BlockToolCall, ID: "c2", Name: "t2", Args: json.RawMessage(`{}`)},
			llm.Block{Type: llm.BlockToolCall, ID: "c3", Name: "t3", Args: json.RawMessage(`{}`)},
		),
		llmtest.Text("all done"),
	)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45,
		Tools: []Tool{mkTool("t1", 1), mkTool("t2", 2), mkTool("t3", 3)}})

	var events []Event
	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventToolEnd {
			ended <- ev.Call.Name
		}
		if ev.Message != nil {
			m := *ev.Message
			ev.Message = &m
		}
		events = append(events, ev)
	}
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}

	var endOrder, resultOrder []string
	for _, ev := range events {
		if ev.Type == EventToolEnd {
			endOrder = append(endOrder, ev.Call.Name)
		}
		if ev.Type == EventMessage && ev.Message.Role == llm.RoleToolResult {
			resultOrder = append(resultOrder, ev.Message.ToolName)
		}
	}
	if !reflect.DeepEqual(endOrder, []string{"t3", "t1", "t2"}) {
		t.Fatalf("tool_end order = %v, want completion order [t3 t1 t2]", endOrder)
	}
	if !reflect.DeepEqual(resultOrder, []string{"t1", "t2", "t3"}) {
		t.Fatalf("result message order = %v, want block order [t1 t2 t3]", resultOrder)
	}
}

func TestSequentialToolForcesBatch(t *testing.T) {
	var concurrent, maxConcurrent atomic.Int32
	var order []string
	var mu sync.Mutex
	mkTool := func(name string, seq bool) Tool {
		return Tool{
			Name: name, Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
			Sequential: seq,
			Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
				c := concurrent.Add(1)
				for {
					m := maxConcurrent.Load()
					if c <= m || maxConcurrent.CompareAndSwap(m, c) {
						break
					}
				}
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				concurrent.Add(-1)
				return Text("ok"), nil
			},
		}
	}
	fake := llmtest.New(
		llmtest.Blocks(llm.StopToolUse,
			llm.Block{Type: llm.BlockToolCall, ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
			llm.Block{Type: llm.BlockToolCall, ID: "b", Name: "t2", Args: json.RawMessage(`{}`)},
			llm.Block{Type: llm.BlockToolCall, ID: "c", Name: "t3", Args: json.RawMessage(`{}`)},
		),
		llmtest.Text("done"),
	)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45,
		Tools: []Tool{mkTool("t1", false), mkTool("t2", true), mkTool("t3", false)}})
	if err := stRunEnd(t, stCollect(s.Run(context.Background(), "go"))).Err; err != nil {
		t.Fatal(err)
	}
	if maxConcurrent.Load() != 1 {
		t.Fatalf("max concurrency = %d, want 1 (one Sequential tool serializes the batch, R33)", maxConcurrent.Load())
	}
	if !reflect.DeepEqual(order, []string{"t1", "t2", "t3"}) {
		t.Fatalf("execution order = %v, want block order", order)
	}
}

func TestUnknownTool(t *testing.T) {
	fake := llmtest.New(llmtest.ToolCall("nope", map[string]any{}), llmtest.Text("recovered"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}})
	events := stCollect(s.Run(context.Background(), "go"))
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for _, ev := range events {
		if ev.Type == EventMessage && ev.Message.Role == llm.RoleToolResult {
			if !ev.Message.IsError || ev.Message.Blocks[0].Text != "unknown tool: nope" {
				t.Fatalf("result = %+v", ev.Message)
			}
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("no error tool result")
	}
	if len(fake.Requests()) != 2 {
		t.Fatal("loop did not continue after unknown tool")
	}
}

// TestToolErrorBecomesResult: a tool whose Execute returns an error produces an
// is_error tool result carrying err.Error(), surfaced on EventToolEnd as
// ToolErr, and the loop continues so the model can react (§10.1).
func TestToolErrorBecomesResult(t *testing.T) {
	boom := NewTool("boom", "always fails",
		func(ctx context.Context, a struct{}) (ToolResult, error) {
			return ToolResult{}, errors.New("disk on fire")
		})
	fake := llmtest.New(llmtest.ToolCall("boom", map[string]any{}), llmtest.Text("recovered"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{boom}})

	events := stCollect(s.Run(context.Background(), "go"))
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}
	var sawToolEnd, sawResult bool
	for _, ev := range events {
		if ev.Type == EventToolEnd {
			if ev.ToolErr == nil || ev.ToolErr.Error() != "disk on fire" {
				t.Fatalf("ToolErr = %v, want the tool's error", ev.ToolErr)
			}
			sawToolEnd = true
		}
		if ev.Type == EventMessage && ev.Message.Role == llm.RoleToolResult {
			if !ev.Message.IsError || ev.Message.Blocks[0].Text != "disk on fire" {
				t.Fatalf("result = %+v, want is_error carrying the message", ev.Message)
			}
			sawResult = true
		}
	}
	if !sawToolEnd || !sawResult {
		t.Fatalf("tool_end=%v result=%v, want both", sawToolEnd, sawResult)
	}
	if len(fake.Requests()) != 2 {
		t.Fatal("loop did not continue after a failing tool")
	}
}

func TestBeforeToolBlocks(t *testing.T) {
	var executed bool
	tool := Tool{
		Name: "guarded", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			executed = true
			return Text("ran"), nil
		},
	}
	fake := llmtest.New(llmtest.ToolCall("guarded", map[string]any{}), llmtest.Text("ok"))
	s := New(Config{
		LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{tool},
		BeforeTool: func(ctx context.Context, call ToolCall) error {
			return fmt.Errorf("blocked by policy: no")
		},
	})
	events := stCollect(s.Run(context.Background(), "go"))
	if executed {
		t.Fatal("Execute ran despite policy block")
	}
	for _, ev := range events {
		if ev.Type == EventToolEnd {
			if ev.ToolErr == nil || ev.ToolErr.Error() != "blocked by policy: no" {
				t.Fatalf("ToolErr = %v", ev.ToolErr)
			}
		}
		if ev.Type == EventMessage && ev.Message.Role == llm.RoleToolResult && !ev.Message.IsError {
			t.Fatal("blocked call produced non-error result")
		}
	}
}

func TestSteering(t *testing.T) {
	gate := make(chan struct{})
	tool := Tool{
		Name: "slow", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			<-gate
			return Text("slow done"), nil
		},
	}
	fake := llmtest.New(llmtest.ToolCall("slow", map[string]any{}), llmtest.Text("ok"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{tool}})

	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventToolStart {
			// The agent is mid-tool: steer twice, then release the tool.
			s.Send("steer one")
			s.Send("steer two")
			close(gate)
		}
		if ev.Type == EventRunEnd && ev.Err != nil {
			t.Fatal(ev.Err)
		}
	}

	reqs := fake.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	// R31: steering injected after the tool result, before the next LLM call,
	// with the whole queue drained together.
	msgs := reqs[1].Messages
	n := len(msgs)
	if msgs[n-2].Role != llm.RoleUser || msgs[n-1].Role != llm.RoleUser {
		t.Fatalf("tail roles = %v", stRoles(msgs))
	}
	if msgs[n-3].Role != llm.RoleToolResult {
		t.Fatalf("steering not after tool result: %v", stRoles(msgs))
	}
	if msgs[n-2].Blocks[0].Text != "steer one" || msgs[n-1].Blocks[0].Text != "steer two" {
		t.Fatalf("steering content wrong: %q %q", msgs[n-2].Blocks[0].Text, msgs[n-1].Blocks[0].Text)
	}
}

func TestFollowUp(t *testing.T) {
	fake := llmtest.New(llmtest.Text("one"), llmtest.Text("two"), llmtest.Text("three"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})
	s.FollowUp("follow 1")
	s.FollowUp("follow 2")

	events := stCollect(s.Run(context.Background(), "start"))
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}
	reqs := fake.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3 (one per follow-up boundary)", len(reqs))
	}
	// One follow-up per boundary (R31): request 2 ends with follow 1,
	// request 3 ends with follow 2.
	last := func(r llm.Request) string { return r.Messages[len(r.Messages)-1].Blocks[0].Text }
	if last(reqs[1]) != "follow 1" || last(reqs[2]) != "follow 2" {
		t.Fatalf("follow-up delivery: %q, %q", last(reqs[1]), last(reqs[2]))
	}
}

func TestSendWhileIdle(t *testing.T) {
	fake := llmtest.New(llmtest.Text("ok"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})
	s.Send("queued early")

	if err := stRunEnd(t, stCollect(s.Run(context.Background(), "the prompt"))).Err; err != nil {
		t.Fatal(err)
	}
	msgs := fake.Requests()[0].Messages
	if len(msgs) != 2 || msgs[0].Blocks[0].Text != "the prompt" || msgs[1].Blocks[0].Text != "queued early" {
		t.Fatalf("first request messages: %+v", msgs)
	}
}

func TestInterruptDuringLLM(t *testing.T) {
	blocker := make(chan struct{})
	fake := llmtest.New(llmtest.Reply{
		Blocks:  []llm.Block{llm.TextBlock("partial thought")},
		Blocker: blocker,
	})
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})

	started := make(chan struct{})
	go func() {
		<-started
		s.Interrupt() // cancels the in-flight stream; Blocker's ctx fires
	}()

	var events []Event
	var once sync.Once
	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventModel && ev.Stream.Type == llm.EventTextEnd {
			once.Do(func() { close(started) })
		}
		events = append(events, ev)
	}
	end := stRunEnd(t, events)
	if !errors.Is(end.Err, ErrInterrupted) {
		t.Fatalf("run_end err = %v, want ErrInterrupted", end.Err)
	}
	st := s.State()
	lastMsg := st.Messages[len(st.Messages)-1]
	if lastMsg.Role != llm.RoleAssistant || lastMsg.StopReason != llm.StopAborted {
		t.Fatalf("last message = %+v", lastMsg)
	}
	if lastMsg.Blocks[0].Text != "partial thought" {
		t.Fatalf("partial content lost: %+v", lastMsg.Blocks)
	}

	// Continue is the retry path after aborts (§11.1).
	fake.Append(llmtest.Text("recovered"))
	msg, err := Final(s.Continue(context.Background()))
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if msg.StopReason != llm.StopEnd {
		t.Fatalf("continued stop = %q", msg.StopReason)
	}
}

func TestInterruptDuringTools(t *testing.T) {
	mkTool := func(name string) Tool {
		return Tool{
			Name: name, Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
			Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
				<-ctx.Done()
				return ToolResult{}, ctx.Err()
			},
		}
	}
	fake := llmtest.New(llmtest.Blocks(llm.StopToolUse,
		llm.Block{Type: llm.BlockToolCall, ID: "x1", Name: "w1", Args: json.RawMessage(`{}`)},
		llm.Block{Type: llm.BlockToolCall, ID: "x2", Name: "w2", Args: json.RawMessage(`{}`)},
	))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{mkTool("w1"), mkTool("w2")}})

	started := make(chan struct{}, 2)
	go func() {
		<-started
		<-started // both tools started
		s.Interrupt()
	}()

	var events []Event
	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventToolStart {
			started <- struct{}{}
		}
		events = append(events, ev)
	}
	if !errors.Is(stRunEnd(t, events).Err, ErrInterrupted) {
		t.Fatalf("err = %v", stRunEnd(t, events).Err)
	}

	// Every tool call answered (R32): history stays valid.
	st := s.State()
	answered := map[string]bool{}
	for _, m := range st.Messages {
		if m.Role == llm.RoleToolResult {
			answered[m.ToolCallID] = true
			if !m.IsError {
				t.Fatalf("interrupted tool result not is_error: %+v", m)
			}
		}
	}
	if !answered["x1"] || !answered["x2"] {
		t.Fatalf("unanswered tool calls: %v", answered)
	}
}

func TestBreakingIteratorInterrupts(t *testing.T) {
	tool := Tool{
		Name: "w", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			<-ctx.Done() // released by the break-triggered cancel (P4)
			return ToolResult{}, ctx.Err()
		},
	}
	fake := llmtest.New(llmtest.ToolCall("w", map[string]any{}))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{tool}})

	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventToolStart {
			break // P4: breaking the iterator interrupts the run
		}
	}

	// History valid: the call is answered even though no more events flowed.
	st := s.State()
	var answered bool
	for _, m := range st.Messages {
		if m.Role == llm.RoleToolResult {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("tool call unanswered after break: %v", stRoles(st.Messages))
	}

	// The session is reusable afterwards.
	fake.Append(llmtest.Text("next run works"))
	if _, err := Final(s.Run(context.Background(), "again")); err != nil {
		t.Fatalf("session unusable after break: %v", err)
	}
}

func TestModelSwitchMidRun(t *testing.T) {
	fake := llmtest.New(llmtest.ToolCall("noop", map[string]any{}), llmtest.Text("done"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}})

	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventTurnEnd && ev.Turn == 1 {
			s.SetModel(llm.GPT5Mini)
			s.SetReasoning(llm.EffortHigh)
		}
	}
	reqs := fake.Requests()
	if reqs[0].Model.ID != "claude-sonnet-4-5" || reqs[1].Model.ID != "gpt-5-mini" {
		t.Fatalf("models = %s, %s", reqs[0].Model.ID, reqs[1].Model.ID)
	}
	if reqs[1].Reasoning != llm.EffortHigh {
		t.Fatalf("reasoning = %q", reqs[1].Reasoning)
	}
}

func TestMaxTurns(t *testing.T) {
	fake := llmtest.New(
		llmtest.ToolCall("noop", map[string]any{}),
		llmtest.ToolCall("noop", map[string]any{}),
		llmtest.ToolCall("noop", map[string]any{}),
		llmtest.ToolCall("noop", map[string]any{}),
	)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}, MaxTurns: 3})
	events := stCollect(s.Run(context.Background(), "loop forever"))
	if !errors.Is(stRunEnd(t, events).Err, ErrMaxTurns) {
		t.Fatalf("err = %v", stRunEnd(t, events).Err)
	}
	if len(fake.Requests()) != 3 {
		t.Fatalf("llm calls = %d, want 3", len(fake.Requests()))
	}
}

func TestBusy(t *testing.T) {
	blocker := make(chan struct{})
	fake := llmtest.New(llmtest.Reply{Blocks: []llm.Block{llm.TextBlock("hi")}, Blocker: blocker})
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var once sync.Once
		var runErr error
		for ev := range s.Run(context.Background(), "first") {
			if ev.Type == EventModel {
				once.Do(func() { close(started) })
			}
			if ev.Type == EventRunEnd {
				runErr = ev.Err
			}
		}
		done <- runErr
	}()

	<-started
	events := stCollect(s.Run(context.Background(), "second"))
	if len(events) != 1 || !errors.Is(events[0].Err, ErrBusy) {
		t.Fatalf("concurrent run events = %+v", events)
	}
	close(blocker)
	if err := <-done; err != nil {
		t.Fatalf("first run err = %v", err)
	}
}

func TestStateSnapshotAndResume(t *testing.T) {
	st := State{
		System:    "sys",
		Model:     llm.ClaudeSonnet45,
		Reasoning: llm.EffortLow,
		Messages: []llm.Message{
			llm.UserText("earlier question"),
			llm.AppMessage("ui-note", json.RawMessage(`{"pinned":true}`)),
			llm.UserText("please continue"),
		},
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var st2 State
	if err := json.Unmarshal(b, &st2); err != nil {
		t.Fatal(err)
	}

	fake := llmtest.New(llmtest.Text("continuing"))
	s := Resume(Config{LLM: fake}, st2)
	if _, err := Final(s.Continue(context.Background())); err != nil {
		t.Fatal(err)
	}
	// The Kind message survives State but never reaches the request (R37).
	req := fake.Requests()[0]
	for _, m := range req.Messages {
		if m.Kind != "" {
			t.Fatalf("Kind message reached the model: %+v", m)
		}
	}
	if req.System != "sys" || req.Reasoning != llm.EffortLow {
		t.Fatalf("resumed config lost: %+v", req)
	}
	final := s.State()
	if final.Messages[1].Kind != "ui-note" {
		t.Fatal("Kind message dropped from state")
	}
}

func TestEventMessageReconstruction(t *testing.T) {
	fake := llmtest.New(llmtest.ToolCall("noop", map[string]any{}), llmtest.Text("done"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}})

	var replayed []llm.Message
	for ev := range s.Run(context.Background(), "go") {
		if ev.Type == EventMessage {
			replayed = append(replayed, *ev.Message)
		}
	}
	got, _ := json.Marshal(replayed)
	want, _ := json.Marshal(s.State().Messages)
	if string(got) != string(want) {
		t.Fatalf("EventMessage replay != State:\n%s\n%s", got, want)
	}
}

func TestLLMErrorEndsRun(t *testing.T) {
	boom := errors.New("provider exploded")
	fake := llmtest.New(llmtest.Error(boom))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})
	events := stCollect(s.Run(context.Background(), "go"))
	if !errors.Is(stRunEnd(t, events).Err, boom) {
		t.Fatalf("run_end err = %v", stRunEnd(t, events).Err)
	}
	last := s.State().Messages[len(s.State().Messages)-1]
	if last.StopReason != llm.StopError || last.ErrorText == "" {
		t.Fatalf("error message not persisted: %+v", last)
	}
}

func TestProgressEvents(t *testing.T) {
	tool := Tool{
		Name: "p", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			Progress(ctx, Text("step 1"))
			Progress(ctx, Text("step 2"))
			return Text("final"), nil
		},
	}
	fake := llmtest.New(llmtest.ToolCall("p", map[string]any{}), llmtest.Text("ok"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{tool}})

	var updates []string
	var endSeen bool
	for ev := range s.Run(context.Background(), "go") {
		switch ev.Type {
		case EventToolUpdate:
			if endSeen {
				t.Fatal("tool_update after tool_end")
			}
			updates = append(updates, ev.Result.Blocks[0].Text)
		case EventToolEnd:
			endSeen = true
		}
	}
	if !reflect.DeepEqual(updates, []string{"step 1", "step 2"}) {
		t.Fatalf("updates = %v", updates)
	}
}

func TestContinueNothingToDo(t *testing.T) {
	fake := llmtest.New(llmtest.Text("hello"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})

	events := stCollect(s.Continue(context.Background()))
	if !errors.Is(stRunEnd(t, events).Err, ErrNothingToDo) {
		t.Fatalf("fresh Continue err = %v", stRunEnd(t, events).Err)
	}

	if _, err := Final(s.Run(context.Background(), "hi")); err != nil {
		t.Fatal(err)
	}
	events = stCollect(s.Continue(context.Background()))
	if !errors.Is(stRunEnd(t, events).Err, ErrNothingToDo) {
		t.Fatalf("post-clean-run Continue err = %v", stRunEnd(t, events).Err)
	}
}

func TestRace(t *testing.T) {
	replies := make([]llmtest.Reply, 0, 6)
	for range 5 {
		replies = append(replies, llmtest.ToolCall("noop", map[string]any{}))
	}
	replies = append(replies, llmtest.Text("done"))
	fake := llmtest.New(replies...)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, Tools: []Tool{stNoop("noop")}})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	hammer := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
	}
	hammer(func() { _ = s.State() })
	hammer(func() { s.SetReasoning(llm.EffortLow) })
	hammer(func() { s.SetModel(llm.ClaudeSonnet45) })

	events := stCollect(s.Run(context.Background(), "go"))
	close(stop)
	wg.Wait()
	if err := stRunEnd(t, events).Err; err != nil {
		t.Fatal(err)
	}
}

func TestFinalHelper(t *testing.T) {
	fake := llmtest.New(llmtest.Text("answer"))
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45})
	msg, err := Final(s.Run(context.Background(), "q"))
	if err != nil || msg.Blocks[0].Text != "answer" {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}

	// Run errors propagate.
	fake2 := llmtest.New(llmtest.Error(errors.New("nope")))
	s2 := New(Config{LLM: fake2, Model: llm.ClaudeSonnet45})
	if _, err := Final(s2.Run(context.Background(), "q")); err == nil {
		t.Fatal("expected error")
	}

	// Busy (no assistant message at all) surfaces as the run error.
	s3 := New(Config{LLM: llmtest.New(), Model: llm.ClaudeSonnet45})
	events := stCollect(s3.Continue(context.Background()))
	if !errors.Is(stRunEnd(t, events).Err, ErrNothingToDo) {
		t.Fatal("expected ErrNothingToDo")
	}
}

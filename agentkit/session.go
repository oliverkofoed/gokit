// Package agentkit is a small library for building agent loops on top of LLM
// providers. The stateless model-call layer lives in the llm subpackage; this
// package adds the single stateful primitive: Session — an agent loop with
// typed tools, steering and follow-up queues, interruption, and mid-run model
// switching. See SPEC.md for the full contract.
package agentkit

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

// Config configures a Session.
type Config struct {
	LLM    llm.Streamer // required: *llm.Client or llmtest.Fake
	Model  llm.Model    // required
	System string
	Tools  []Tool

	Reasoning llm.Effort

	// BeforeTool runs after validation, before Execute. Returning a non-nil
	// error blocks the call: Execute is skipped and the error becomes an
	// is_error tool result the model sees. Policy enforcement hook.
	BeforeTool func(ctx context.Context, call ToolCall) error

	// MaxTurns caps LLM calls per Run (runaway protection). 0 → 40.
	MaxTurns int
}

const defaultMaxTurns = 40

// State is the JSON-serializable snapshot of a session (R36).
type State struct {
	System    string        `json:"system"`
	Model     llm.Model     `json:"model"`
	Reasoning llm.Effort    `json:"reasoning"`
	Messages  []llm.Message `json:"messages"`
}

// Session is the stateful agent loop. Send, SendMessage, FollowUp, Interrupt,
// SetModel, SetReasoning, ClearQueues and State are safe from any goroutine
// at any time; Run/RunMessage/Continue are mutually exclusive (R35).
type Session struct {
	streamer   llm.Streamer
	beforeTool func(ctx context.Context, call ToolCall) error
	maxTurns   int
	tools      []Tool
	toolIndex  map[string]Tool

	running atomic.Bool

	mu          sync.Mutex
	system      string
	model       llm.Model
	reasoning   llm.Effort
	messages    []llm.Message
	steering    []llm.Message
	followUp    []llm.Message
	cancelRun   context.CancelFunc
	interrupted bool
}

// New builds a Session. It panics on duplicate tool names — a programmer
// error best caught at startup.
func New(cfg Config) *Session {
	return Resume(cfg, State{
		System:    cfg.System,
		Model:     cfg.Model,
		Reasoning: cfg.Reasoning,
	})
}

// Resume rebuilds a session from a snapshot (R36). The snapshot supplies
// System/Model/Reasoning/Messages; cfg supplies LLM, Tools, hooks, MaxTurns.
func Resume(cfg Config, st State) *Session {
	if cfg.LLM == nil {
		panic("agentkit: Config.LLM is required")
	}
	s := &Session{
		streamer:   cfg.LLM,
		beforeTool: cfg.BeforeTool,
		maxTurns:   cfg.MaxTurns,
		tools:      cfg.Tools,
		toolIndex:  map[string]Tool{},
		system:     st.System,
		model:      st.Model,
		reasoning:  st.Reasoning,
		messages:   deepCopyMessages(st.Messages),
	}
	if s.maxTurns <= 0 {
		s.maxTurns = defaultMaxTurns
	}
	for _, t := range cfg.Tools {
		if _, dup := s.toolIndex[t.Name]; dup {
			panic(fmt.Sprintf("agentkit: duplicate tool name %q", t.Name))
		}
		s.toolIndex[t.Name] = t
	}
	return s
}

// State returns a deep-copied snapshot; safe mid-run (R36).
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{
		System:    s.system,
		Model:     s.model,
		Reasoning: s.reasoning,
		Messages:  deepCopyMessages(s.messages),
	}
}

// Send queues a steering message: it is appended to history at the next turn
// boundary of the active run (R31). If idle, the next Run/Continue drains it
// before its first LLM call.
func (s *Session) Send(prompt string) { s.SendMessage(llm.UserText(prompt)) }

// SendMessage queues a steering message (see Send).
func (s *Session) SendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steering = append(s.steering, msg)
}

// FollowUp queues a message delivered only when the run would otherwise
// finish; the run then continues with it instead of ending. Follow-ups are
// consumed one per boundary (R31).
func (s *Session) FollowUp(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.followUp = append(s.followUp, llm.UserText(prompt))
}

// Interrupt aborts the in-flight LLM call or tool batch (R32). The partial
// assistant message is kept with StopAborted; the run ends. No-op when idle.
func (s *Session) Interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelRun != nil {
		s.interrupted = true
		s.cancelRun()
	}
}

// SetModel switches models; takes effect at the next LLM call, mid-run
// included (R34). Cross-model history handoff is automatic.
func (s *Session) SetModel(m llm.Model) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = m
}

// SetReasoning switches the reasoning effort; takes effect at the next LLM
// call.
func (s *Session) SetReasoning(e llm.Effort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasoning = e
}

// ClearQueues drops all queued steering and follow-up messages.
func (s *Session) ClearQueues() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steering = nil
	s.followUp = nil
}

// Run appends a user message and executes the loop, yielding events as they
// happen. The loop runs inside the iterator (pull model, P4): breaking early
// interrupts the run. Only one Run/RunMessage/Continue may be active; a
// concurrent call yields a single run_end event with Err = ErrBusy.
func (s *Session) Run(ctx context.Context, prompt string) iter.Seq[Event] {
	return s.RunMessage(ctx, llm.UserText(prompt))
}

// RunMessage is Run with an explicit initial message.
func (s *Session) RunMessage(ctx context.Context, msg llm.Message) iter.Seq[Event] {
	return s.loop(ctx, &msg)
}

// Continue re-enters the loop without a new prompt: after an error or abort,
// or to drain queued messages. Yields run_end with Err = ErrNothingToDo if
// there is nothing to continue from.
func (s *Session) Continue(ctx context.Context) iter.Seq[Event] {
	return s.loop(ctx, nil)
}

// ---- the loop (SPEC §11.3) --------------------------------------------------

func (s *Session) loop(parent context.Context, initial *llm.Message) iter.Seq[Event] {
	return func(yield func(Event) bool) {
		if !s.running.CompareAndSwap(false, true) {
			yield(Event{Type: EventRunEnd, Err: ErrBusy})
			return
		}
		defer s.running.Store(false)

		runCtx, cancel := context.WithCancel(parent)
		defer cancel()
		s.mu.Lock()
		s.cancelRun = cancel
		s.interrupted = false
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.cancelRun = nil
			s.mu.Unlock()
		}()

		// stopped means the consumer broke out of the range: no further yields
		// are allowed, the run aborts (P4), but history must stay valid.
		stopped := false
		turn := 0
		emit := func(ev Event) bool {
			if stopped {
				return false
			}
			ev.Turn = turn
			if !yield(ev) {
				stopped = true
				cancel()
				return false
			}
			return true
		}
		endRun := func(err error) { emit(Event{Type: EventRunEnd, Err: err}) }

		if initial == nil && !s.canContinue() {
			emit(Event{Type: EventRunStart})
			endRun(ErrNothingToDo)
			return
		}

		emit(Event{Type: EventRunStart})
		if initial != nil {
			s.appendAndEmit(*initial, emit)
		}

		for {
			// Steering drains completely before each request build: this is
			// the turn-boundary injection of R31 plus the idle-queue pickup.
			for _, m := range s.drainSteering() {
				s.appendAndEmit(m, emit)
			}

			turn++
			if turn > s.maxTurns {
				endRun(ErrMaxTurns)
				return
			}
			emit(Event{Type: EventTurnStart})

			stream := s.streamer.Stream(runCtx, s.buildRequest())
			for ev := range stream.Events() {
				e := ev
				if !emit(Event{Type: EventModel, Stream: &e}) {
					break
				}
			}
			msg, llmErr := stream.Message()
			if len(msg.Blocks) > 0 || msg.StopReason == llm.StopError || msg.StopReason == llm.StopAborted {
				s.appendAndEmit(msg, emit)
			}

			calls := toolCalls(msg)

			if stopped || msg.StopReason == llm.StopAborted || msg.StopReason == llm.StopError {
				// Abnormal end: answer any tool calls so history stays valid
				// (R32), then finish.
				for _, call := range calls {
					s.appendAndEmit(interruptedResult(call), emit)
				}
				switch {
				case stopped:
					// Consumer broke out: no more events can be yielded.
				case msg.StopReason == llm.StopError:
					endRun(llmErr)
				default:
					endRun(s.abortErr(parent))
				}
				return
			}

			if len(calls) > 0 {
				outcomes := s.runBatch(runCtx, calls, emit)
				for i, call := range calls {
					s.appendAndEmit(resultMessage(call, outcomes[i]), emit)
				}
				if runCtx.Err() != nil {
					if !stopped {
						endRun(s.abortErr(parent))
					}
					return
				}
			}

			emit(Event{Type: EventTurnEnd})
			if stopped {
				return
			}

			if s.steeringPending() || len(calls) > 0 {
				continue
			}
			if fu, ok := s.popFollowUp(); ok {
				s.appendAndEmit(fu, emit)
				continue
			}
			endRun(nil)
			return
		}
	}
}

func (s *Session) canContinue() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steering) > 0 || len(s.followUp) > 0 {
		return true
	}
	if len(s.messages) == 0 {
		return false
	}
	// Walk past Kind messages: they are inert to the loop (R37).
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Kind != "" {
			continue
		}
		if m.Role == llm.RoleUser || m.Role == llm.RoleToolResult {
			return true
		}
		// An assistant tail is continuable only when it ended abnormally —
		// Continue doubles as the retry path after errors and aborts (§11.1).
		return m.StopReason == llm.StopError || m.StopReason == llm.StopAborted
	}
	return false
}

func (s *Session) buildRequest() llm.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	defs := make([]llm.ToolDef, 0, len(s.tools))
	for _, t := range s.tools {
		defs = append(defs, llm.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return llm.Request{
		Model:     s.model,
		System:    s.system,
		Messages:  append([]llm.Message(nil), s.messages...),
		Tools:     defs,
		Reasoning: s.reasoning,
	}
}

// appendAndEmit appends msg to history and announces it (R30). The emitted
// Message is a copy, safe to retain.
func (s *Session) appendAndEmit(msg llm.Message, emit func(Event) bool) {
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	cp := msg
	emit(Event{Type: EventMessage, Message: &cp})
}

func (s *Session) drainSteering() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.steering
	s.steering = nil
	return out
}

func (s *Session) steeringPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steering) > 0
}

func (s *Session) popFollowUp() (llm.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.followUp) == 0 {
		return llm.Message{}, false
	}
	m := s.followUp[0]
	s.followUp = s.followUp[1:]
	return m, true
}

// abortErr attributes an aborted run: parent cancellation wins, then an
// explicit Interrupt(), then (defensively) ErrInterrupted.
func (s *Session) abortErr(parent context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	return ErrInterrupted
}

// ---- tool execution (R33) ----------------------------------------------------

type toolOutcome struct {
	result ToolResult
	err    error
}

// runBatch executes a batch of tool calls: concurrently, unless any called
// tool is Sequential or unknown-tool handling makes order matter — per R33,
// one Sequential tool serializes the whole batch. tool_start/tool_end events
// fire in real execution order; the caller appends result messages in
// assistant block order afterward.
func (s *Session) runBatch(ctx context.Context, calls []ToolCall, emit func(Event) bool) []toolOutcome {
	sequential := false
	for _, call := range calls {
		if t, ok := s.toolIndex[call.Name]; ok && t.Sequential {
			sequential = true
			break
		}
	}

	outcomes := make([]toolOutcome, len(calls))
	events := make(chan Event, 16)

	go func() {
		defer close(events)
		if sequential || len(calls) == 1 {
			for i, call := range calls {
				outcomes[i] = s.execTool(ctx, call, events)
			}
			return
		}
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call ToolCall) {
				defer wg.Done()
				outcomes[i] = s.execTool(ctx, call, events)
			}(i, call)
		}
		wg.Wait()
	}()

	// Forward tool events from the single consumer goroutine. Even after the
	// consumer stops, keep draining so workers never block.
	for ev := range events {
		emit(ev)
	}
	return outcomes
}

func (s *Session) execTool(ctx context.Context, call ToolCall, events chan<- Event) toolOutcome {
	callCopy := call
	events <- Event{Type: EventToolStart, Call: &callCopy}
	out := s.execToolInner(ctx, call, events)
	end := Event{Type: EventToolEnd, Call: &callCopy, ToolErr: out.err}
	if out.err == nil {
		r := out.result
		end.Result = &r
	}
	events <- end
	return out
}

func (s *Session) execToolInner(ctx context.Context, call ToolCall, events chan<- Event) (out toolOutcome) {
	if ctx.Err() != nil {
		return toolOutcome{err: fmt.Errorf("interrupted before execution")}
	}
	tool, ok := s.toolIndex[call.Name]
	if !ok {
		return toolOutcome{err: fmt.Errorf("unknown tool: %s", call.Name)}
	}
	// §11.3 order: validate, BeforeTool, Execute.
	if tool.validate != nil {
		if err := tool.validate(call.Args); err != nil {
			return toolOutcome{err: err}
		}
	}
	if s.beforeTool != nil {
		if err := s.beforeTool(ctx, call); err != nil {
			return toolOutcome{err: err}
		}
	}

	callCopy := call
	pctx := context.WithValue(ctx, progressKey{}, progressFunc(func(update ToolResult) {
		u := update
		events <- Event{Type: EventToolUpdate, Call: &callCopy, Result: &u}
	}))

	defer func() {
		if r := recover(); r != nil {
			out = toolOutcome{err: fmt.Errorf("tool panic: %v", r)}
		}
	}()
	result, err := tool.Execute(pctx, call)
	return toolOutcome{result: result, err: err}
}

func toolCalls(msg llm.Message) []ToolCall {
	var calls []ToolCall
	for _, b := range msg.Blocks {
		if b.Type == llm.BlockToolCall {
			calls = append(calls, ToolCall{ID: b.ID, Name: b.Name, Args: b.Args})
		}
	}
	return calls
}

func resultMessage(call ToolCall, out toolOutcome) llm.Message {
	if out.err != nil {
		return llm.ToolResultMessage(call.ID, call.Name, true, llm.TextBlock(out.err.Error()))
	}
	return llm.ToolResultMessage(call.ID, call.Name, false, out.result.Blocks...)
}

func interruptedResult(call ToolCall) llm.Message {
	return llm.ToolResultMessage(call.ID, call.Name, true, llm.TextBlock("interrupted before execution"))
}

func deepCopyMessages(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		c := m
		c.Blocks = make([]llm.Block, len(m.Blocks))
		copy(c.Blocks, m.Blocks)
		for j := range c.Blocks {
			if len(c.Blocks[j].Args) > 0 {
				c.Blocks[j].Args = append([]byte(nil), c.Blocks[j].Args...)
			}
		}
		if len(m.Meta) > 0 {
			c.Meta = append([]byte(nil), m.Meta...)
		}
		if m.Usage != nil {
			u := *m.Usage
			c.Usage = &u
		}
		out[i] = c
	}
	return out
}

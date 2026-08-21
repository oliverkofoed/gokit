package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"
)

// Streamer is the minimal capability the agent layer depends on. *Client
// implements it; llmtest.Fake implements it.
type Streamer interface {
	Stream(ctx context.Context, req Request) *Stream
}

// Stream is a single in-flight completion. The request executes as the
// consumer pulls (P4): ranging Events() runs the producer inline; breaking
// early cancels the request.
type Stream struct {
	model   Model
	produce func(emit func(Event) bool)

	started  bool
	done     bool
	final    Message
	finalErr error
}

// NewStream builds a Stream around a producer (R10). produce runs in the
// calling goroutine of Events()/Message(). It must emit a valid raw event
// sequence per R6 — content events plus exactly one DoneEvent or ErrorEvent —
// and return promptly when emit returns false.
//
// NewStream wraps produce with accumulator bookkeeping: it maintains the
// partial Message, fills Event.Message on every event, fills Event.Block on
// *_end events, computes usage cost, synthesizes the final message on
// Done/Error, and guarantees exactly one terminal event (appending an
// EventError if produce returns without one or panics).
//
// Raw event contract for producers:
//   - Event{Type: EventStart} is emitted by the caller of the producer (the
//     client emits it before dispatch); producers built directly on NewStream
//     (fakes) should emit it first themselves.
//   - *_start events open block Index. Tool-call starts should carry
//     Block: &Block{Type: BlockToolCall, ID: ..., Name: ...}.
//   - *_delta events append. Tool-call deltas carry raw JSON fragments in
//     Delta; the accumulator maintains the best-effort partial parse (R7).
//   - *_end events close block Index. A non-nil Block on an end event
//     overrides accumulated fields (non-zero fields win) — use it to attach
//     signatures, IDs, or authoritative content.
//   - DoneEvent/ErrorEvent terminate.
func NewStream(model Model, produce func(emit func(Event) bool)) *Stream {
	return &Stream{model: model, produce: produce}
}

// Events iterates the stream. It may be ranged at most once (R9); a second
// range yields nothing. Breaking early cancels the request; the final state
// is then available via Message with StopReason StopAborted.
func (s *Stream) Events() iter.Seq[Event] {
	return func(yield func(Event) bool) { s.run(yield) }
}

// Message drains any remaining events and returns the final assistant
// message. err is non-nil iff StopReason is StopError or StopAborted; the
// message is returned in both cases with partial content preserved (P5).
func (s *Stream) Message() (Message, error) {
	if !s.done {
		s.run(nil)
	}
	return s.final, s.finalErr
}

func (s *Stream) run(yield func(Event) bool) {
	if s.started {
		return // R9: single consumer; second range yields nothing
	}
	s.started = true

	acc := newAccumulator(s.model)
	terminal := false
	consumerStopped := false

	emit := func(ev Event) bool {
		// After the terminal event — or after the consumer broke out (the
		// stream is aborted at that point, R9) — further raw events are
		// ignored entirely so a rogue producer cannot alter the outcome.
		if terminal || consumerStopped {
			return false
		}
		ev = acc.apply(ev)
		if ev.Type == EventDone || ev.Type == EventError {
			terminal = true
		}
		if yield != nil && !yield(ev) {
			consumerStopped = true
			return false
		}
		return true
	}

	func() {
		defer func() {
			if r := recover(); r != nil && !terminal {
				emitErr := fmt.Errorf("llm: stream producer panic: %v", r)
				ev := acc.apply(ErrorEvent(emitErr, acc.usage))
				terminal = true
				if !consumerStopped && yield != nil {
					yield(ev)
				}
			}
		}()
		s.produce(emit)
	}()

	if !terminal {
		var err error
		if consumerStopped {
			err = fmt.Errorf("llm: stream aborted by consumer: %w", context.Canceled)
		} else {
			err = errors.New("llm: stream ended without a terminal event")
		}
		ev := acc.apply(ErrorEvent(err, acc.usage))
		// R6/R10: the event stream carries exactly one terminal event, so the
		// synthesized one must reach an active consumer — same as the panic
		// path above. A consumer that broke out is no longer listening.
		if !consumerStopped && yield != nil {
			yield(ev)
		}
	}

	s.final, s.finalErr = acc.msg, acc.err
	s.done = true
}

// accumulator builds the partial/final Message from raw events.
type accumulator struct {
	model    Model
	msg      Message
	err      error
	argsBufs map[int]*[]byte // accumulated tool-call JSON fragments per index
	usage    Usage
}

func newAccumulator(model Model) *accumulator {
	return &accumulator{
		model: model,
		msg: Message{
			Role:     RoleAssistant,
			Model:    model.ID,
			Provider: model.Provider,
			API:      model.API,
			Time:     time.Now(),
		},
		argsBufs: map[int]*[]byte{},
	}
}

// apply folds a raw event into the message and returns the enriched event.
func (a *accumulator) apply(ev Event) Event {
	switch ev.Type {
	case EventStart:
		// nothing to fold

	case EventTextStart:
		a.ensureBlock(ev.Index, BlockText)
		a.mergeBlock(ev.Index, ev.Block)
	case EventThinkingStart:
		a.ensureBlock(ev.Index, BlockThinking)
		a.mergeBlock(ev.Index, ev.Block)
	case EventToolCallStart:
		b := a.ensureBlock(ev.Index, BlockToolCall)
		b.Args = json.RawMessage("{}")
		a.mergeBlock(ev.Index, ev.Block)

	case EventTextDelta, EventThinkingDelta:
		b := a.ensureBlock(ev.Index, blockTypeForDelta(ev.Type))
		b.Text += ev.Delta
		b.Signature += ev.signatureDelta
	case EventToolCallDelta:
		b := a.ensureBlock(ev.Index, BlockToolCall)
		buf := a.argsBufs[ev.Index]
		if buf == nil {
			buf = new([]byte)
			a.argsBufs[ev.Index] = buf
		}
		*buf = append(*buf, ev.Delta...)
		b.Args = parsePartialJSON(string(*buf)) // R7: best-effort partial parse

	case EventTextEnd, EventThinkingEnd:
		a.ensureBlock(ev.Index, blockTypeForDelta(ev.Type))
		a.mergeBlock(ev.Index, ev.Block)
		ev.Block = a.blockCopy(ev.Index)
	case EventToolCallEnd:
		b := a.ensureBlock(ev.Index, BlockToolCall)
		if buf := a.argsBufs[ev.Index]; buf != nil && len(*buf) > 0 {
			b.Args = parsePartialJSON(string(*buf))
		}
		a.mergeBlock(ev.Index, ev.Block)
		ev.Block = a.blockCopy(ev.Index)

	case EventDone:
		if ev.usage != nil {
			a.usage = *ev.usage
		}
		a.usage.TotalCost = ComputeCost(a.model, a.usage)
		u := a.usage
		a.msg.Usage = &u
		a.msg.StopReason = ev.stopReason
		if a.msg.StopReason == "" {
			a.msg.StopReason = StopEnd
		}

	case EventError:
		if ev.usage != nil && (ev.usage.Input != 0 || ev.usage.Output != 0 || ev.usage.CacheRead != 0 || ev.usage.CacheWrite != 0) {
			a.usage = *ev.usage
		}
		a.usage.TotalCost = ComputeCost(a.model, a.usage)
		u := a.usage
		a.msg.Usage = &u
		if isAbortErr(ev.Err) {
			a.msg.StopReason = StopAborted
		} else {
			a.msg.StopReason = StopError
		}
		if ev.Err != nil {
			a.msg.ErrorText = ev.Err.Error()
		}
		a.err = ev.Err
	}

	ev.Message = &a.msg
	return ev
}

func blockTypeForDelta(t EventType) BlockType {
	switch t {
	case EventThinkingDelta, EventThinkingStart, EventThinkingEnd:
		return BlockThinking
	default:
		return BlockText
	}
}

// ensureBlock grows Blocks so index exists, creating it (and any gap blocks)
// with the given type, and returns a pointer to it.
func (a *accumulator) ensureBlock(index int, t BlockType) *Block {
	for len(a.msg.Blocks) <= index {
		a.msg.Blocks = append(a.msg.Blocks, Block{})
	}
	b := &a.msg.Blocks[index]
	if b.Type == "" {
		b.Type = t
	}
	return b
}

// mergeBlock folds a producer-provided block override into the accumulated
// block: non-zero fields of the override win; Text wins only when non-empty.
func (a *accumulator) mergeBlock(index int, override *Block) {
	if override == nil {
		return
	}
	b := &a.msg.Blocks[index]
	if override.Type != "" {
		b.Type = override.Type
	}
	if override.Text != "" {
		b.Text = override.Text
	}
	if override.Signature != "" {
		b.Signature = override.Signature
	}
	if override.Redacted {
		b.Redacted = true
	}
	if override.Data != "" {
		b.Data = override.Data
	}
	if override.MimeType != "" {
		b.MimeType = override.MimeType
	}
	if override.ID != "" {
		b.ID = override.ID
	}
	if override.Name != "" {
		b.Name = override.Name
	}
	if len(override.Args) > 0 {
		b.Args = override.Args
	}
}

func (a *accumulator) blockCopy(index int) *Block {
	b := a.msg.Blocks[index]
	return &b
}

func isAbortErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

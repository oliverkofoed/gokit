package llm

// EventType discriminates stream events.
type EventType string

const (
	EventStart         EventType = "start" // always first
	EventTextStart     EventType = "text_start"
	EventTextDelta     EventType = "text_delta"
	EventTextEnd       EventType = "text_end"
	EventThinkingStart EventType = "thinking_start"
	EventThinkingDelta EventType = "thinking_delta"
	EventThinkingEnd   EventType = "thinking_end"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallDelta EventType = "tool_call_delta"
	EventToolCallEnd   EventType = "tool_call_end"
	EventDone          EventType = "done"  // terminal success
	EventError         EventType = "error" // terminal failure (incl. abort)
)

// Event is one streaming event (P6: tagged struct). See R6 for the ordering
// contract and R7 for tool-call delta semantics.
type Event struct {
	Type  EventType
	Index int    // content block index this event pertains to
	Delta string // incremental text on *_delta (tool_call_delta: raw JSON fragment)

	// Block is the completed block, set on text_end / thinking_end /
	// tool_call_end.
	Block *Block

	// Message is the partial message so far, on every event; on EventDone and
	// EventError it is the final message. It is a live accumulator owned by
	// the stream — copy it if you retain it past the current iteration.
	Message *Message

	// Err is set on EventError. errors.Is(Err, context.Canceled) distinguishes
	// aborts; Message.StopReason is StopAborted vs StopError accordingly.
	Err error

	// Producer-side fields, consumed by the stream accumulator (R10).
	// Same-package protocol implementations may set signatureDelta directly on
	// thinking deltas; external producers (llmtest) use the constructors below
	// and Block overrides on *_end events.
	usage          *Usage
	stopReason     StopReason
	signatureDelta string
}

// DoneEvent builds the terminal success event for a stream producer (R10).
// stop must be StopEnd, StopLength, or StopToolUse; usage carries the token
// counts (cost is computed by the accumulator from the model's price table).
func DoneEvent(stop StopReason, usage Usage) Event {
	u := usage
	return Event{Type: EventDone, stopReason: stop, usage: &u}
}

// ErrorEvent builds the terminal failure event for a stream producer (R10).
// The accumulator derives StopAborted (context cancellation) vs StopError
// from err. Partial usage may be zero.
func ErrorEvent(err error, usage Usage) Event {
	u := usage
	return Event{Type: EventError, Err: err, usage: &u}
}

package agentkit

import (
	"errors"
	"iter"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

// EventType discriminates session events (SPEC §11.2).
type EventType string

const (
	EventRunStart   EventType = "run_start"
	EventTurnStart  EventType = "turn_start" // one LLM call + its tool batch
	EventModel      EventType = "model"      // wraps one llm.Event (deltas etc.)
	EventMessage    EventType = "message"    // a message was appended to history
	EventToolStart  EventType = "tool_start"
	EventToolUpdate EventType = "tool_update" // via agentkit.Progress
	EventToolEnd    EventType = "tool_end"
	EventTurnEnd    EventType = "turn_end"
	EventRunEnd     EventType = "run_end" // always the last event
)

// Event is one session event (P6: tagged struct).
type Event struct {
	Type EventType
	Turn int // 1-based turn counter within the run

	Stream  *llm.Event   // EventModel
	Message *llm.Message // EventMessage: the appended message (a copy; safe to retain)
	Call    *ToolCall    // EventToolStart/Update/End
	Result  *ToolResult  // EventToolUpdate (partial) / EventToolEnd (final)
	ToolErr error        // EventToolEnd when the tool failed or was blocked

	// Err on EventRunEnd: nil = clean finish; otherwise ErrBusy, ErrMaxTurns,
	// ErrNothingToDo, ErrInterrupted, ctx.Err(), or the LLM error.
	Err error
}

var (
	ErrBusy        = errors.New("agentkit: a run is already active")
	ErrNothingToDo = errors.New("agentkit: nothing to continue")
	ErrMaxTurns    = errors.New("agentkit: max turns exceeded")
	ErrInterrupted = errors.New("agentkit: run interrupted")
)

// Final drains an event sequence and returns the last assistant message.
// err is the run_end error if the run failed; if no assistant message was
// produced, err is at least ErrNothingToDo.
func Final(events iter.Seq[Event]) (llm.Message, error) {
	var last llm.Message
	var sawAssistant bool
	var runErr error
	for ev := range events {
		if ev.Type == EventMessage && ev.Message != nil && ev.Message.Role == llm.RoleAssistant {
			last = *ev.Message
			sawAssistant = true
		}
		if ev.Type == EventRunEnd {
			runErr = ev.Err
		}
	}
	if runErr != nil {
		return last, runErr
	}
	if !sawAssistant {
		return last, ErrNothingToDo
	}
	return last, nil
}

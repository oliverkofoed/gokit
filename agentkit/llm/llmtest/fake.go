// Package llmtest provides a deterministic, zero-network llm.Streamer for
// agent-loop tests (SPEC §9). It builds streams through llm.NewStream (R26),
// so fake streams exercise the same accumulator code paths as real ones.
package llmtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

// Reply scripts one assistant response.
type Reply struct {
	Blocks []llm.Block
	// StopReason of the reply. "" infers: StopToolUse if any tool_call block,
	// else StopEnd.
	StopReason llm.StopReason
	// Err, if set, ends the stream with an EventError (after any Blocks).
	Err error
	// Blocker, if set, is waited on before the terminal event — tests use it
	// to deterministically overlap Send/Interrupt/SetModel with an in-flight
	// call (R27). Context cancellation still wins during the wait.
	Blocker <-chan struct{}
}

// Text scripts a plain text reply.
func Text(text string) Reply {
	return Reply{Blocks: []llm.Block{llm.TextBlock(text)}}
}

// ToolCall scripts a reply calling one tool; args is json.Marshal-ed.
func ToolCall(name string, args any) Reply {
	b, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("llmtest.ToolCall: marshal args: %v", err))
	}
	return Reply{Blocks: []llm.Block{{Type: llm.BlockToolCall, Name: name, Args: b}}}
}

// Blocks scripts a reply from explicit blocks with an explicit stop reason.
func Blocks(stop llm.StopReason, blocks ...llm.Block) Reply {
	return Reply{Blocks: blocks, StopReason: stop}
}

// Error scripts a failing reply.
func Error(err error) Reply {
	return Reply{Err: err}
}

// Fake implements llm.Streamer with a scripted queue of replies. An empty
// queue yields an EventError ("llmtest: no replies queued") — a loud test
// failure, not a hang. All methods are safe for concurrent use.
type Fake struct {
	mu     sync.Mutex
	queue  []Reply
	reqs   []llm.Request
	callID int
}

var _ llm.Streamer = (*Fake)(nil)

// New builds a Fake with an initial reply queue.
func New(replies ...Reply) *Fake {
	return &Fake{queue: replies}
}

// Append adds replies to the live queue (safe mid-run, from any goroutine).
func (f *Fake) Append(replies ...Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, replies...)
}

// Requests returns a copy of every llm.Request received so far — assert on
// system prompt, message history, model ID, tools. Messages reflect the
// normalized, as-sent view (llm.Normalize), exactly as a real provider would
// receive them — Kind messages absent, orphaned tool calls answered, etc.
func (f *Fake) Requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]llm.Request, len(f.reqs))
	copy(out, f.reqs)
	return out
}

// Stream implements llm.Streamer: it pops the next Reply and synthesizes a
// valid event sequence per R6 — text/thinking split into word-level deltas,
// tool-call args streamed as two JSON fragments, usage estimated at
// len(text)/4 tokens. It honors ctx cancellation between events (StopAborted).
func (f *Fake) Stream(ctx context.Context, req llm.Request) *llm.Stream {
	f.mu.Lock()
	reqCopy := req
	reqCopy.Messages = llm.Normalize(req.Model, req.Messages)
	reqCopy.Tools = append([]llm.ToolDef(nil), req.Tools...)
	f.reqs = append(f.reqs, reqCopy)

	var reply Reply
	hasReply := len(f.queue) > 0
	if hasReply {
		reply = f.queue[0]
		f.queue = f.queue[1:]
	}
	f.mu.Unlock()

	return llm.NewStream(req.Model, func(emit func(llm.Event) bool) {
		if !emit(llm.Event{Type: llm.EventStart}) {
			return
		}
		if !hasReply {
			emit(llm.ErrorEvent(errors.New("llmtest: no replies queued"), llm.Usage{}))
			return
		}

		usage := llm.Usage{Input: estimateTokens(req), Output: estimateOutput(reply)}
		aborted := func() bool {
			if ctx.Err() != nil {
				emit(llm.ErrorEvent(ctx.Err(), usage))
				return true
			}
			return false
		}

		for i, b := range reply.Blocks {
			if aborted() {
				return
			}
			switch b.Type {
			case llm.BlockText, llm.BlockThinking:
				start, delta, end := llm.EventTextStart, llm.EventTextDelta, llm.EventTextEnd
				if b.Type == llm.BlockThinking {
					start, delta, end = llm.EventThinkingStart, llm.EventThinkingDelta, llm.EventThinkingEnd
				}
				if !emit(llm.Event{Type: start, Index: i}) {
					return
				}
				for _, chunk := range splitWords(b.Text) {
					if aborted() {
						return
					}
					if !emit(llm.Event{Type: delta, Index: i, Delta: chunk}) {
						return
					}
				}
				endBlock := b
				if !emit(llm.Event{Type: end, Index: i, Block: &endBlock}) {
					return
				}
			case llm.BlockToolCall:
				id := b.ID
				if id == "" {
					f.mu.Lock()
					f.callID++
					id = fmt.Sprintf("call_%d", f.callID)
					f.mu.Unlock()
				}
				if !emit(llm.Event{Type: llm.EventToolCallStart, Index: i,
					Block: &llm.Block{Type: llm.BlockToolCall, ID: id, Name: b.Name}}) {
					return
				}
				args := string(b.Args)
				if args == "" {
					args = "{}"
				}
				half := len(args) / 2
				for _, frag := range []string{args[:half], args[half:]} {
					if frag == "" {
						continue
					}
					if aborted() {
						return
					}
					if !emit(llm.Event{Type: llm.EventToolCallDelta, Index: i, Delta: frag}) {
						return
					}
				}
				if !emit(llm.Event{Type: llm.EventToolCallEnd, Index: i}) {
					return
				}
			default:
				// Other block types (images in assistant replies) are attached
				// whole via a text_end override — rare, but keeps tests honest.
				endBlock := b
				if !emit(llm.Event{Type: llm.EventTextEnd, Index: i, Block: &endBlock}) {
					return
				}
			}
		}

		if reply.Blocker != nil {
			select {
			case <-ctx.Done():
				emit(llm.ErrorEvent(ctx.Err(), usage))
				return
			case <-reply.Blocker:
			}
		}
		if aborted() {
			return
		}
		if reply.Err != nil {
			emit(llm.ErrorEvent(reply.Err, usage))
			return
		}
		stop := reply.StopReason
		if stop == "" {
			stop = llm.StopEnd
			for _, b := range reply.Blocks {
				if b.Type == llm.BlockToolCall {
					stop = llm.StopToolUse
					break
				}
			}
		}
		emit(llm.DoneEvent(stop, usage))
	})
}

// splitWords chunks text at word boundaries, keeping separators, so deltas
// resemble real streaming.
func splitWords(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i, r := range s {
		if r == ' ' || r == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func estimateTokens(req llm.Request) int {
	n := len(req.System)
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			n += len(b.Text) + len(b.Args)
		}
	}
	return n / 4
}

func estimateOutput(r Reply) int {
	var b strings.Builder
	for _, blk := range r.Blocks {
		b.WriteString(blk.Text)
		b.Write(blk.Args)
	}
	return b.Len() / 4
}

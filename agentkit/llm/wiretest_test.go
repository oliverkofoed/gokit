package llm

// Shared test helpers for protocol tests. Protocol-specific test files must
// prefix their own helpers (anth*, oaic*, oair*, gem*) to avoid collisions —
// anything generic belongs here.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// captureTransport records the last request and serves a scripted response.
// Use it for golden payload tests.
type captureTransport struct {
	reqs     []*transport.Request
	status   int
	header   http.Header
	chunks   []string
	failWith error
}

func (t *captureTransport) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
	cp := *req
	cp.Body = append([]byte(nil), req.Body...)
	t.reqs = append(t.reqs, &cp)
	if t.failWith != nil {
		return nil, t.failWith
	}
	status := t.status
	if status == 0 {
		status = 200
	}
	h := t.header
	if h == nil {
		h = http.Header{"Content-Type": []string{"text/event-stream"}}
	}
	return &transport.Response{Status: status, Header: h, Body: newChunkReader(ctx, t.chunks)}, nil
}

func (t *captureTransport) lastReq(tb testing.TB) *transport.Request {
	tb.Helper()
	if len(t.reqs) == 0 {
		tb.Fatal("no request captured")
	}
	return t.reqs[len(t.reqs)-1]
}

// lastBody unmarshals the last captured request body.
func (t *captureTransport) lastBody(tb testing.TB) map[string]any {
	tb.Helper()
	var m map[string]any
	if err := json.Unmarshal(t.lastReq(tb).Body, &m); err != nil {
		tb.Fatalf("unmarshal captured body: %v\nbody: %s", err, t.lastReq(tb).Body)
	}
	return m
}

// chunkReader yields one scripted chunk per Read and honors ctx cancellation
// between chunks (so abort-mid-stream tests are deterministic).
type chunkReader struct {
	ctx    context.Context
	chunks []string
	pos    int
}

func newChunkReader(ctx context.Context, chunks []string) *chunkReader {
	return &chunkReader{ctx: ctx, chunks: chunks}
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.pos >= len(r.chunks) {
		return 0, io.EOF
	}
	c := r.chunks[r.pos]
	if len(c) > len(p) {
		n := copy(p, c)
		r.chunks[r.pos] = c[n:]
		return n, nil
	}
	r.pos++
	return copy(p, c), nil
}

func (r *chunkReader) Close() error { return nil }

// collectEvents drains a stream, returning all events (Message pointers are
// snapshotted so later mutation doesn't confuse assertions).
func collectEvents(s *Stream) []Event {
	var out []Event
	for ev := range s.Events() {
		if ev.Message != nil {
			m := *ev.Message
			ev.Message = &m
		}
		out = append(out, ev)
	}
	return out
}

// eventTypes projects the Type sequence for order assertions.
func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

// textOf concatenates all text-block content of a message.
func textOf(m Message) string {
	var b strings.Builder
	for _, blk := range m.Blocks {
		if blk.Type == BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// sseChunk formats one SSE event as a single chunk.
func sseChunk(event, data string) string {
	if event == "" {
		return "data: " + data + "\n\n"
	}
	return "event: " + event + "\ndata: " + data + "\n\n"
}

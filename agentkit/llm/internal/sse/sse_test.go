package sse

// Tests for SSE framing (SPEC §12.4).

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// readAllEvents drains a reader, returning events and the terminating error.
func readAllEvents(r *Reader) ([]Event, error) {
	var out []Event
	for {
		ev, err := r.Next()
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}

func TestSSE(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Event
	}{
		{"single event", "data: hello\n\n", []Event{{Data: "hello"}}},
		{"multi-line data joined with newline", "data: a\ndata: b\ndata: c\n\n", []Event{{Data: "a\nb\nc"}}},
		{"event name", "event: message_start\ndata: x\n\n", []Event{{Name: "message_start", Data: "x"}}},
		{"event name only", "event: ping\n\n", []Event{{Name: "ping"}}},
		{"comment lines ignored", ": keepalive\ndata: x\n: another\n\n", []Event{{Data: "x"}}},
		{"comment-only stream", ": one\n: two\n\n", nil},
		{"CRLF line endings", "event: e\r\ndata: x\r\n\r\n", []Event{{Name: "e", Data: "x"}}},
		{"missing trailing newline before EOF", "data: tail", []Event{{Data: "tail"}}},
		{"missing blank line before EOF", "data: tail\n", []Event{{Data: "tail"}}},
		{"missing blank line before EOF with name", "event: e\ndata: tail\n", []Event{{Name: "e", Data: "tail"}}},
		{"id and retry ignored", "id: 42\nretry: 100\ndata: x\n\n", []Event{{Data: "x"}}},
		{"unknown field ignored", "wat: huh\ndata: x\n\n", []Event{{Data: "x"}}},
		{"no space after colon", "data:x\n\n", []Event{{Data: "x"}}},
		{"only one leading space stripped", "data:  two spaces\n\n", []Event{{Data: " two spaces"}}},
		{"empty data field", "data:\n\n", []Event{{Data: ""}}},
		{"blank-line separation", "data: 1\n\ndata: 2\n\nevent: e\ndata: 3\n\n",
			[]Event{{Data: "1"}, {Data: "2"}, {Name: "e", Data: "3"}}},
		{"extra blank lines between events", "\n\ndata: 1\n\n\n\ndata: 2\n\n",
			[]Event{{Data: "1"}, {Data: "2"}}},
		{"DONE marker is ordinary data", "data: [DONE]\n\n", []Event{{Data: "[DONE]"}}},
		{"empty input", "", nil},
		{"unicode data", "data: héllo 😀\n\n", []Event{{Data: "héllo 😀"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAllEvents(NewReader(strings.NewReader(tc.in)))
			if err != io.EOF {
				t.Fatalf("terminating error = %v, want io.EOF", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("events = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSSEHugeEvent: a single data line well past 64KiB must parse — this is
// the bufio.Scanner token-limit trap the reader must not have.
func TestSSEHugeEvent(t *testing.T) {
	payload := strings.Repeat("a", 100<<10) // 100 KiB on one line
	in := "data: " + payload + "\n\ndata: after\n\n"
	events, err := readAllEvents(NewReader(strings.NewReader(in)))
	if err != io.EOF {
		t.Fatalf("terminating error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Data != payload {
		t.Fatalf("huge event corrupted: len %d, want %d", len(events[0].Data), len(payload))
	}
	if events[1].Data != "after" {
		t.Fatalf("event after huge event = %+v", events[1])
	}
}

// TestSSESizeCap: an event over 32 MiB fails with ErrEventTooLarge instead of
// consuming unbounded memory.
func TestSSESizeCap(t *testing.T) {
	huge := io.MultiReader(
		strings.NewReader("data: "),
		strings.NewReader(strings.Repeat("a", 33<<20)), // 33 MiB
		strings.NewReader("\n\n"),
	)
	_, err := readAllEvents(NewReader(huge))
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("err = %v, want ErrEventTooLarge", err)
	}
}

// TestSSESizeCapAcrossLines: the cap applies to the whole accumulated event,
// not just a single line.
func TestSSESizeCapAcrossLines(t *testing.T) {
	line := "data: " + strings.Repeat("b", 8<<20) + "\n" // ~8 MiB per line
	huge := io.MultiReader(
		strings.NewReader(strings.Repeat(line, 5)), // ~40 MiB in one event
		strings.NewReader("\n"),
	)
	_, err := readAllEvents(NewReader(huge))
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("err = %v, want ErrEventTooLarge", err)
	}
}

// TestSSEUnderlyingError: a mid-stream read error surfaces as-is.
func TestSSEUnderlyingError(t *testing.T) {
	sentinel := errors.New("connection reset")
	r := NewReader(io.MultiReader(strings.NewReader("data: one\n\n"), errReader{sentinel}))
	ev, err := r.Next()
	if err != nil || ev.Data != "one" {
		t.Fatalf("first event = %+v, %v", ev, err)
	}
	if _, err := r.Next(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// FuzzSSE: the reader never panics and always terminates on arbitrary input.
func FuzzSSE(f *testing.F) {
	f.Add([]byte("data: hello\n\n"))
	f.Add([]byte("data: a\ndata: b\n\n"))
	f.Add([]byte("event: e\r\ndata: x\r\n\r\n"))
	f.Add([]byte(": comment\nid: 1\nretry: 2\nwat: 3\ndata:\n\n"))
	f.Add([]byte("data: tail"))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte("data"))
	f.Add([]byte(":::::"))
	f.Add([]byte{0xff, 0xfe, 0x00, '\n', '\n'})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		for i := 0; ; i++ {
			_, err := r.Next()
			if err != nil {
				break
			}
			if i > len(data) {
				t.Fatalf("more events than input bytes: reader is not consuming input")
			}
		}
	})
}

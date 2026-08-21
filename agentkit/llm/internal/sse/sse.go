// Package sse parses text/event-stream framing. It is deliberately small:
// event name + data accumulation, comment and unknown-field tolerance, CRLF
// tolerance, and a hard cap on event size so adversarial input cannot consume
// unbounded memory.
package sse

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Event is one server-sent event. Data for multi-line events is joined with
// "\n" per the SSE specification.
type Event struct {
	Name string // the "event:" field, "" if absent
	Data string
}

// ErrEventTooLarge is returned when a single event exceeds the size cap.
var ErrEventTooLarge = errors.New("sse: event exceeds size limit")

const maxEventSize = 32 << 20 // 32 MiB, defensive

// Reader decodes events from an SSE byte stream.
type Reader struct {
	br *bufio.Reader
}

// NewReader wraps r for event decoding.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r)}
}

// Next returns the next event. It returns io.EOF at clean end of stream and
// the underlying read error otherwise. A final event unterminated by a blank
// line is still delivered before io.EOF.
func (r *Reader) Next() (Event, error) {
	var (
		name    string
		data    []string
		sawData bool
		size    int
	)
	for {
		line, err := r.readLine()
		if err != nil {
			if err == io.EOF && (sawData || name != "") {
				return Event{Name: name, Data: strings.Join(data, "\n")}, nil
			}
			return Event{}, err
		}
		size += len(line)
		if size > maxEventSize {
			return Event{}, ErrEventTooLarge
		}

		if line == "" { // blank line: dispatch if anything accumulated
			if sawData || name != "" {
				return Event{Name: name, Data: strings.Join(data, "\n")}, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // comment
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data = append(data, value)
			sawData = true
		default:
			// id:, retry:, unknown fields: ignored
		}
	}
}

// readLine reads one line, stripping the trailing \n and optional \r. At EOF
// with a non-empty unterminated line, the line is returned with nil error and
// the following call returns io.EOF.
func (r *Reader) readLine() (string, error) {
	line, err := r.br.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return strings.TrimSuffix(line, "\r"), nil
		}
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

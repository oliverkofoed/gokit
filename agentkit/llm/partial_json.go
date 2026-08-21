package llm

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// parsePartialJSON best-effort parses a JSON prefix (R7): it completes the
// longest valid prefix by closing open strings, arrays, and objects, and
// trimming dangling tokens. The result is always valid JSON; at minimum "{}".
func parsePartialJSON(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	completed := completeJSON(s)
	if json.Valid([]byte(completed)) {
		return json.RawMessage(completed)
	}
	return json.RawMessage("{}")
}

// completeJSON attempts to repair a truncated JSON document.
func completeJSON(s string) string {
	var (
		stack    []byte // '{' and '['
		inString bool
		escape   bool
	)
	// tokenStart marks the beginning of the trailing incomplete non-string
	// token (bare literal or number), -1 when none.
	tokenStart := -1

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			tokenStart = -1
		case '{', '[':
			stack = append(stack, c)
			tokenStart = -1
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			tokenStart = -1
		case ',', ':', ' ', '\t', '\n', '\r':
			tokenStart = -1
		default:
			if tokenStart == -1 {
				tokenStart = i
			}
		}
	}

	if inString {
		// Drop a trailing incomplete escape sequence or split UTF-8 rune,
		// then close the string.
		if escape {
			s = s[:len(s)-1]
		}
		s = trimIncompleteUTF8(s)
		s = trimIncompleteUnicodeEscape(s)
		s += `"`
	} else if tokenStart >= 0 {
		// A trailing bare token (true/false/null/number) may be truncated.
		token := s[tokenStart:]
		if !isCompleteScalar(token) {
			s = s[:tokenStart]
		}
	}

	// Strip a dangling comma, or a dangling "key": with no value.
	s = strings.TrimRight(s, " \t\n\r")
	s = strings.TrimSuffix(s, ",")
	if strings.HasSuffix(strings.TrimRight(s, " \t\n\r"), ":") {
		s = strings.TrimRight(s, " \t\n\r") + " null"
	}

	// If the last string was an object key with no colon, give it a value.
	// Detect: inside an object, content ends with a closed string that is a
	// key position. Rather than track key/value state, rely on json.Valid
	// after both candidate repairs.
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			s += "}"
		} else {
			s += "]"
		}
	}
	if json.Valid([]byte(s)) {
		return s
	}
	// Second chance: the trailing closed string may have been a dangling key
	// ({"a": 1, "b"} is invalid). Insert ": null" before the closers.
	trimmed := strings.TrimRight(s, "}]")
	closers := s[len(trimmed):]
	candidate := trimmed + ": null" + closers
	if json.Valid([]byte(candidate)) {
		return candidate
	}
	return "{}"
}

func trimIncompleteUTF8(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			s = s[:len(s)-1]
			continue
		}
		return s
	}
	return s
}

// trimIncompleteUnicodeEscape drops a trailing partial \uXXXX escape.
func trimIncompleteUnicodeEscape(s string) string {
	// Look for a backslash-u within the last 5 bytes that isn't followed by
	// four hex digits.
	for i := max(0, len(s)-6); i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == 'u' && len(s)-i < 6 {
			// Verify the backslash itself isn't escaped.
			backslashes := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				return s[:i]
			}
		}
	}
	return s
}

func isCompleteScalar(token string) bool {
	switch token {
	case "true", "false", "null":
		return true
	}
	// Numbers: valid on their own is good enough; a truncated exponent or
	// bare minus fails json.Valid and gets trimmed.
	return json.Valid([]byte(token))
}

package textkit

import "unicode"

// Shorten makes the given input string fit the max length, and optionally appends "..." (ellipsis)
func Shorten(input string, maxLength int, ellipsis bool) string {
	if len(input) <= maxLength {
		return input
	}

	// adjust maxLength to make room for ellipsis
	if ellipsis {
		maxLength = maxLength - 3 // 3 = "...".length
		if maxLength < 0 {
			maxLength = 0
		}

	}

	// find end point
	end := 0
	for i, r := range input {
		if unicode.IsSpace(r) {
			if i <= maxLength {
				end = i
			}
		}
	}

	if !ellipsis {
		return string(input[:end])
	}

	buf := make([]byte, end+3, end+3)
	copy(buf, input[:end])
	copy(buf[end:], "...")
	return string(buf)
}

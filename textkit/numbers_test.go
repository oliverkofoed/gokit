package textkit

import (
	"strings"
	"testing"
)

func TestShortNumber(t *testing.T) {
	entries := []struct {
		fill   string
		input  int
		output string
	}{
		{"        ", 1, "   1"},
		{"        ", 9, "   9"},
		{"       ", 10, "  10"},
		{"      ", 120, " 120"},
		{"      ", 992, " 992"},
		{"     ", 1003, "  1K"},
		{"     ", 1300, "1.3K"},
		{"     ", 1900, "1.9K"},
		{"     ", 1990, "  2K"},
		{"     ", 9000, "  9K"},
		{"    ", 10000, " 10K"},
		{"    ", 90000, " 90K"},
		{"   ", 900000, "900K"},
		{"  ", 1000000, "  1M"},
		{"  ", 1200000, "1.2M"},
		{" ", 12000000, " 12M"},
		{"", 120000000, "120M"},
	}

	for _, x := range entries {
		result := ShortNumber(x.input)
		if result != strings.Trim(x.output, " ") {
			t.Errorf("Input: %v, expected %v, got: '%v'", x.input, strings.Trim(x.output, " "), result)
			return
		}
	}
}

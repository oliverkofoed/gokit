package textkit

import "testing"

func TestShorten(t *testing.T) {
	entries := []struct {
		input     string
		maxLength int
		ellipsis  bool
		output    string
	}{
		{"Hello world", 3, true, "..."},
		{"Hello world", 8, true, "Hello..."},
		{"Hello world", 28, true, "Hello world"},
		{"Hello world", 3, false, ""},
		{"Hello world", 8, false, "Hello"},
		{"Hello world", 28, false, "Hello world"},
	}

	for _, x := range entries {
		result := Shorten(x.input, x.maxLength, x.ellipsis)
		if result != x.output {
			t.Error("Input: %v, max:%v, ellipsis:%v was %v, but expected %v", x.input, x.maxLength, x.ellipsis, result, x.output)
			return
		}
	}
}

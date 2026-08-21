package llm

// Tests for the internal partial-JSON parser (SPEC R7): parse the longest
// valid prefix, auto-closing open strings/arrays/objects; always valid JSON,
// at minimum "{}".

import (
	"encoding/json"
	"testing"
)

// partialJSONCases: want == "" means "only assert validity", otherwise the
// output must match exactly.
var partialJSONCases = []struct {
	name string
	in   string
	want string
}{
	{"empty input", "", "{}"},
	{"whitespace only", "  \n\t", "{}"},
	{"complete object passes through", `{"a":1}`, `{"a":1}`},
	{"complete nested passes through", `{"a":[1,{"b":"c"}]}`, `{"a":[1,{"b":"c"}]}`},
	{"truncated string", `{"a":"hel`, `{"a":"hel"}`},
	{"bare open object", `{`, `{}`},
	{"bare open array", `[`, `[]`},
	{"open array with elements", `{"a":[1,2`, `{"a":[1,2]}`},
	{"open array trailing comma", `[1,2,`, `[1,2]`},
	{"deep nesting", `{"a":{"b":[{"c":"d`, `{"a":{"b":[{"c":"d"}]}}`},
	{"dangling comma", `{"a":1,`, `{"a":1}`},
	{"dangling key", `{"a":1,"b"`, `{"a":1,"b": null}`},
	{"dangling colon", `{"a":1,"b":`, `{"a":1,"b": null}`},
	{"partial true literal", `{"a":tru`, `{"a": null}`},
	{"partial false literal", `{"a":fal`, `{"a": null}`},
	{"partial null literal", `{"a":nul`, `{"a": null}`},
	{"complete literal kept", `{"a":true`, `{"a":true}`},
	{"complete number kept", `{"a":12`, `{"a":12}`},
	{"truncated number", `{"a":1.`, `{"a": null}`},
	{"truncated exponent", `{"a":1e`, `{"a": null}`},
	{"partial unicode escape", `{"a":"\u00`, `{"a":""}`},
	{"unicode escape complete", `{"a":"é`, `{"a":"é"}`},
	{"trailing backslash", `{"a":"x\`, `{"a":"x"}`},
	{"escaped backslash then partial escape", `{"a":"x\\\u0`, `{"a":"x\\"}`},
	{"split multi-byte rune", "{\"a\":\"caf\xc3", `{"a":"caf"}`},
	{"split four-byte rune", "{\"a\":\"ok\xf0\x9f\x98", `{"a":"ok"}`},
	{"complete multi-byte rune kept", `{"a":"café`, `{"a":"café"}`},
	{"garbage", `not json`, `{}`},
	{"garbage braces", `}{`, `{}`},
	{"bare truncated string", `"hel`, `"hel"`},
	{"escaped quote inside string", `{"a":"he said \"hi`, `{"a":"he said \"hi"}`},
	{"nested arrays", `[[1,[2,[3`, `[[1,[2,[3]]]]`},
	{"string containing braces", `{"code":"if (x) { y[`, `{"code":"if (x) { y["}`},
}

func TestPartialJSON(t *testing.T) {
	for _, tc := range partialJSONCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePartialJSON(tc.in)
			if len(got) == 0 {
				t.Fatal("empty result: must be at minimum {}")
			}
			if !json.Valid(got) {
				t.Fatalf("parsePartialJSON(%q) = %s: not valid JSON", tc.in, got)
			}
			if tc.want != "" && string(got) != tc.want {
				t.Errorf("parsePartialJSON(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestPartialJSONPrefixes replays a realistic tool-call argument byte stream
// and asserts every prefix parses to valid JSON — the exact situation R7
// describes (Args updated on every tool_call_delta).
func TestPartialJSONPrefixes(t *testing.T) {
	full := `{"path":"/tmp/héllo 😀.txt","n":42,"opts":{"create":true,"tags":["a","b"]},"note":"say \"hi\"é"}`
	for i := 0; i <= len(full); i++ {
		got := parsePartialJSON(full[:i])
		if !json.Valid(got) {
			t.Fatalf("prefix %d %q -> %s: not valid JSON", i, full[:i], got)
		}
	}
	if got := parsePartialJSON(full); string(got) != full {
		t.Errorf("complete input altered: %s", got)
	}
}

// FuzzPartialJSON: never panics, always returns valid JSON (R7).
func FuzzPartialJSON(f *testing.F) {
	for _, tc := range partialJSONCases {
		f.Add(tc.in)
	}
	f.Add(`{"a":"\ud83d`)           // lone surrogate escape
	f.Add(`{"a":"😀`)                // emoji then truncation
	f.Add("\x00\xff{\"a\":")        // binary junk prefix
	f.Add(`{"a":[{"b":[{"c":[1,2,`) // deep open nesting
	f.Fuzz(func(t *testing.T, in string) {
		got := parsePartialJSON(in)
		if len(got) == 0 {
			t.Fatalf("parsePartialJSON(%q): empty result", in)
		}
		if !json.Valid(got) {
			t.Fatalf("parsePartialJSON(%q) = %s: not valid JSON", in, got)
		}
	})
}

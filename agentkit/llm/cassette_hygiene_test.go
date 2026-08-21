package llm

// Cassette hygiene (SPEC milestone 7, R23). Recorded cassettes are committed,
// so a leaked key would be permanent. This scans every committed cassette for
// credential shapes and unredacted auth headers before they can ship.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// anthSecretShapes matches the credential formats the four supported
// providers issue. Matching on shape rather than on a known key means this
// catches keys that were never in this process's environment.
var anthSecretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"anthropic", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`)},
	{"openai", regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9_\-]{32,}`)},
	{"google", regexp.MustCompile(`AIza[A-Za-z0-9_\-]{20,}`)},
	{"openrouter", regexp.MustCompile(`sk-or-v1-[A-Za-z0-9]{16,}`)},
}

// anthDeadCassetteMarkers are provider errors that mean the recording never
// captured real protocol behavior. The recorder writes on cleanup even when a
// test fails — deliberately, so an assertion bug can be fixed and replayed
// without paying to record again — but that also means a run against an
// unfunded or unauthorized account silently commits a cassette full of
// billing errors as if it were ground truth.
var anthDeadCassetteMarkers = []string{
	"insufficient_quota",
	"credit_balance_exhausted",
	"no credits remaining",
	"exceeded your current quota",
	"invalid_api_key",
	"authentication_error",
}

func TestCassetteHygiene(t *testing.T) {
	dir := filepath.Join("testdata", "cassettes")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		t.Skipf("no cassettes recorded yet (%s)", dir)
	}
	if err != nil {
		t.Fatal(err)
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		checked++
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)

		for _, shape := range anthSecretShapes {
			if m := shape.re.FindString(text); m != "" {
				// Print only the prefix: the test output is itself a log.
				t.Errorf("%s: %s API key leaked (starts %q) — recording must redact it (R23)",
					path, shape.name, m[:min(len(m), 12)]+"...")
			}
		}
		// The redaction path must have run: any auth header present must
		// have been overwritten, and Gemini's ?key= masked. The identity
		// headers providers echo back are not credentials, but cassettes are
		// public and need not name the account that recorded them.
		for _, header := range []string{
			`"X-Api-Key"`, `"Authorization"`, `"X-Goog-Api-Key"`,
			`"Openai-Organization"`, `"Openai-Project"`,
			`"Anthropic-Organization-Id"`, `"Anthropic-Workspace-Id"`,
			`"Request-Id"`, `"X-Request-Id"`, `"Cf-Ray"`,
		} {
			idx := strings.Index(text, header)
			if idx < 0 {
				continue
			}
			tail := text[idx:min(idx+200, len(text))]
			if !strings.Contains(tail, "REDACTED") {
				t.Errorf("%s: %s present without REDACTED value (R23):\n%s", path, header, tail)
			}
		}
		if strings.Contains(text, "key=") && !strings.Contains(text, "key=REDACTED") {
			t.Errorf("%s: url carries an unmasked key= parameter (R23)", path)
		}
		// A cassette recorded against a broken account proves nothing and
		// must never be committed as ground truth.
		for _, marker := range anthDeadCassetteMarkers {
			if strings.Contains(text, marker) {
				t.Errorf("%s: recorded a %q error, not real protocol traffic — delete it and re-record once the account works",
					path, marker)
				break
			}
		}
	}
	t.Logf("scanned %d cassette(s) in %s", checked, dir)
}

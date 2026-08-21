// Package cassette records and replays provider traffic at the transport
// layer (SPEC §7). Cassettes contain real recorded interactions and are
// committed to the repo; tests replay them with zero network and zero keys.
//
// In tests, use New: it replays testdata/cassettes/<name>.json, or records it
// when the RECORD environment variable is non-empty:
//
//	client := llm.New(
//	    llm.WithTransport(cassette.New(t, "anthropic_tool_call")),
//	    llm.WithAPIKey("anthropic", apiKeyOrDummy()),
//	)
package cassette

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// New returns a replaying transport for testdata/cassettes/<name>.json. If
// the RECORD environment variable is non-empty, it instead records through
// transport.HTTP() and (re)writes the file on test cleanup. Replay failures
// (missing file, request mismatch, unconsumed interactions) fail the test.
func New(t testing.TB, name string) transport.Interface {
	t.Helper()
	path := filepath.Join("testdata", "cassettes", name+".json")
	if os.Getenv("RECORD") != "" {
		rec := Record(path, transport.HTTP())
		t.Cleanup(func() {
			if err := rec.Close(); err != nil {
				t.Errorf("cassette: writing %s: %v", path, err)
			}
		})
		return rec
	}
	return replayPath(t, path)
}

// Replay always replays; it fails the test if the cassette is missing.
func Replay(t testing.TB, name string) transport.Interface {
	t.Helper()
	return replayPath(t, filepath.Join("testdata", "cassettes", name+".json"))
}

// Recorder is a recording transport; Close writes the cassette file.
type Recorder interface {
	transport.Interface
	io.Closer
}

// Record returns a transport that records through inner and writes the
// cassette to path on Close. Sensitive headers and key query parameters are
// redacted on write (R23).
func Record(path string, inner transport.Interface) Recorder {
	return &recorder{path: path, inner: inner}
}

// ---- file format (SPEC §7.2) ----------------------------------------------

type cassetteFile struct {
	Version      int           `json:"version"`
	Interactions []interaction `json:"interactions"`
}

type interaction struct {
	Request  reqRecord  `json:"request"`
	Response respRecord `json:"response"`
}

type reqRecord struct {
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Header   http.Header     `json:"header,omitempty"`
	BodyJSON json.RawMessage `json:"body_json,omitempty"`
	BodyB64  string          `json:"body_b64,omitempty"`
}

type respRecord struct {
	Status    int         `json:"status"`
	Header    http.Header `json:"header,omitempty"`
	Chunks    []string    `json:"chunks,omitempty"`
	ChunksB64 []string    `json:"chunks_b64,omitempty"`
}

// redactedHeaders are overwritten with "REDACTED" on write (R23). The first
// group is credentials; the second is account and request identity that
// providers echo back on responses — never secret, but cassettes are committed
// to a public repo and there is no reason to publish whose account recorded
// them.
var redactedHeaders = []string{
	"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "Set-Cookie",
	"Openai-Organization", "Openai-Project",
	"Anthropic-Organization-Id", "Anthropic-Workspace-Id",
	"Request-Id", "X-Request-Id", "Cf-Ray",
}

func redactHeader(h http.Header) http.Header {
	out := http.Header{}
	for k, vs := range h {
		if isRedactedHeader(k) {
			out[http.CanonicalHeaderKey(k)] = []string{"REDACTED"}
			continue
		}
		out[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
	}
	return out
}

func isRedactedHeader(k string) bool {
	for _, r := range redactedHeaders {
		if strings.EqualFold(k, r) {
			return true
		}
	}
	return false
}

// maskURL rewrites a key=... query parameter to key=REDACTED (Gemini carries
// its API key in the query). Used for both storage and comparison.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	if q.Has("key") {
		q.Set("key", "REDACTED")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// ---- recording --------------------------------------------------------------

type recorder struct {
	path  string
	inner transport.Interface

	mu           sync.Mutex
	interactions []*recordingInteraction
}

type recordingInteraction struct {
	req    reqRecord
	status int
	header http.Header
	chunks [][]byte
}

func (r *recorder) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
	rec := &recordingInteraction{req: recordRequest(req)}
	r.mu.Lock()
	r.interactions = append(r.interactions, rec) // request start order (R23)
	r.mu.Unlock()

	resp, err := r.inner.Do(ctx, req)
	if err != nil {
		// Failed request: drop the placeholder — transport errors are not
		// replayable interactions.
		r.mu.Lock()
		for i, ri := range r.interactions {
			if ri == rec {
				r.interactions = append(r.interactions[:i], r.interactions[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		return nil, err
	}
	rec.status = resp.Status
	rec.header = redactHeader(resp.Header)
	return &transport.Response{
		Status: resp.Status,
		Header: resp.Header,
		Body:   &recordingBody{inner: resp.Body, rec: rec, mu: &r.mu},
	}, nil
}

func recordRequest(req *transport.Request) reqRecord {
	rec := reqRecord{
		Method: req.Method,
		URL:    maskURL(req.URL),
		Header: redactHeader(req.Header),
	}
	ct := req.Header.Get("Content-Type")
	if strings.Contains(ct, "json") && json.Valid(req.Body) {
		var pretty any
		_ = json.Unmarshal(req.Body, &pretty)
		b, err := json.Marshal(pretty)
		if err == nil {
			rec.BodyJSON = b
			return rec
		}
	}
	if len(req.Body) > 0 {
		rec.BodyB64 = base64.StdEncoding.EncodeToString(req.Body)
	}
	return rec
}

// recordingBody captures each Read that returned data as one chunk,
// preserving streaming boundaries (R23).
type recordingBody struct {
	inner io.ReadCloser
	rec   *recordingInteraction
	mu    *sync.Mutex
}

func (b *recordingBody) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	if n > 0 {
		b.mu.Lock()
		b.rec.chunks = append(b.rec.chunks, append([]byte(nil), p[:n]...))
		b.mu.Unlock()
	}
	return n, err
}

func (b *recordingBody) Close() error { return b.inner.Close() }

// Close writes the cassette file.
func (r *recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	file := cassetteFile{Version: 1}
	for _, rec := range r.interactions {
		resp := respRecord{Status: rec.status, Header: rec.header}
		allText := true
		for _, c := range rec.chunks {
			if !utf8.Valid(c) {
				allText = false
				break
			}
		}
		for _, c := range rec.chunks {
			if allText {
				resp.Chunks = append(resp.Chunks, string(c))
			} else {
				resp.ChunksB64 = append(resp.ChunksB64, base64.StdEncoding.EncodeToString(c))
			}
		}
		file.Interactions = append(file.Interactions, interaction{Request: rec.req, Response: resp})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(data, '\n'), 0o644)
}

// ---- replay -----------------------------------------------------------------

type replayer struct {
	t    testing.TB
	path string

	mu   sync.Mutex
	next int
	file cassetteFile
}

func replayPath(t testing.TB, path string) transport.Interface {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cassette: %v (record it with: RECORD=1 go test -run %s)", err, t.Name())
	}
	var file cassetteFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("cassette: parse %s: %v", path, err)
	}
	r := &replayer{t: t, path: path, file: file}
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.next != len(r.file.Interactions) {
			t.Errorf("cassette: %d of %d interactions consumed in %s",
				r.next, len(r.file.Interactions), path)
		}
	})
	return r
}

func (r *replayer) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
	r.mu.Lock()
	if r.next >= len(r.file.Interactions) {
		r.mu.Unlock()
		r.t.Fatalf("cassette: request beyond recorded interactions (%d) in %s: %s %s",
			len(r.file.Interactions), r.path, req.Method, req.URL)
		return nil, context.Canceled // unreachable under testing.TB
	}
	ia := r.file.Interactions[r.next]
	r.next++
	r.mu.Unlock()

	r.match(ia.Request, req)

	var chunks []string
	if len(ia.Response.ChunksB64) > 0 {
		for _, c := range ia.Response.ChunksB64 {
			b, err := base64.StdEncoding.DecodeString(c)
			if err != nil {
				r.t.Fatalf("cassette: bad chunk encoding in %s: %v", r.path, err)
			}
			chunks = append(chunks, string(b))
		}
	} else {
		chunks = ia.Response.Chunks
	}
	return &transport.Response{
		Status: ia.Response.Status,
		Header: ia.Response.Header.Clone(),
		Body:   &replayBody{ctx: ctx, chunks: chunks},
	}, nil
}

// match verifies method, masked URL, and body (R24). Headers are ignored:
// method+URL+body is sufficient and keeps cassettes robust to header churn.
func (r *replayer) match(want reqRecord, got *transport.Request) {
	r.t.Helper()
	if want.Method != got.Method || want.URL != maskURL(got.URL) {
		r.t.Fatalf("cassette: request mismatch in %s:\nwant %s %s\ngot  %s %s",
			r.path, want.Method, want.URL, got.Method, maskURL(got.URL))
	}
	if len(want.BodyJSON) > 0 {
		wantCanon, ok1 := canonicalJSON(want.BodyJSON)
		gotCanon, ok2 := canonicalJSON(got.Body)
		if !ok1 || !ok2 || wantCanon != gotCanon {
			r.t.Fatalf("cassette: body mismatch in %s:\n%s", r.path, jsonDiff(want.BodyJSON, got.Body))
		}
		return
	}
	wantBody, _ := base64.StdEncoding.DecodeString(want.BodyB64)
	if string(wantBody) != string(got.Body) {
		r.t.Fatalf("cassette: body mismatch in %s:\nwant: %q\ngot:  %q", r.path, wantBody, got.Body)
	}
}

// canonicalJSON re-marshals JSON so map key order is irrelevant (R24).
func canonicalJSON(b []byte) (string, bool) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// jsonDiff renders a simple line diff of two pretty-printed JSON bodies.
func jsonDiff(want, got []byte) string {
	wl := prettyLines(want)
	gl := prettyLines(got)
	var b strings.Builder
	max := len(wl)
	if len(gl) > max {
		max = len(gl)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		switch {
		case w == g:
			b.WriteString("  " + w + "\n")
		default:
			if w != "" {
				b.WriteString("- " + w + "\n")
			}
			if g != "" {
				b.WriteString("+ " + g + "\n")
			}
		}
	}
	return b.String()
}

func prettyLines(b []byte) []string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return strings.Split(string(b), "\n")
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return strings.Split(string(b), "\n")
	}
	return strings.Split(string(out), "\n")
}

// replayBody delivers one recorded chunk per Read (R24), so SSE parsing is
// exercised against real chunk boundaries. It honors ctx between chunks so
// abort-mid-stream tests replay deterministically.
type replayBody struct {
	ctx    context.Context
	chunks []string
	pos    int
	offset int
}

func (b *replayBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.pos >= len(b.chunks) {
		return 0, io.EOF
	}
	c := b.chunks[b.pos][b.offset:]
	n := copy(p, c)
	if n == len(c) {
		b.pos++
		b.offset = 0
	} else {
		b.offset += n
	}
	return n, nil
}

func (b *replayBody) Close() error { return nil }

package cassette

// Tests for record/replay (SPEC R23, R24, R25) against httptest servers.
// No sleeps: the streaming server is synchronized with channels.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// ---- fakeT: capture Fatalf/Errorf so we can assert on cassette *failures* ---

// fakeFatal is the sentinel panic payload thrown by fakeT.Fatalf, standing in
// for testing's runtime.Goexit.
type fakeFatal struct{}

// fakeT records failures instead of failing the real test. It embeds
// testing.TB for interface satisfaction; only the methods cassette uses are
// overridden.
type fakeT struct {
	testing.TB
	mu       sync.Mutex
	fatals   []string
	errs     []string
	cleanups []func()
}

func newFakeT(t *testing.T) *fakeT { return &fakeT{TB: t} }

func (f *fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.mu.Lock()
	f.fatals = append(f.fatals, fmt.Sprintf(format, args...))
	f.mu.Unlock()
	panic(fakeFatal{})
}

func (f *fakeT) Errorf(format string, args ...any) {
	f.mu.Lock()
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

func (f *fakeT) Cleanup(fn func()) {
	f.mu.Lock()
	f.cleanups = append(f.cleanups, fn)
	f.mu.Unlock()
}

func (f *fakeT) runCleanups() {
	f.mu.Lock()
	fns := append([]func(){}, f.cleanups...)
	f.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

// expectFatal runs fn, requiring it to die in fakeT.Fatalf (observed as the
// fakeFatal panic). Assert on the message afterwards via lastFatal.
func expectFatal(t *testing.T, ft *fakeT, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fakeFatal); !ok {
				panic(r)
			}
		}
	}()
	fn()
	t.Fatal("expected a cassette Fatalf, got none")
}

func (f *fakeT) lastFatal(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fatals) == 0 {
		t.Fatal("no Fatalf recorded")
	}
	return f.fatals[len(f.fatals)-1]
}

// ---- helpers ----------------------------------------------------------------

// readByReads drains r, returning each Read's bytes as one element — the
// probe for chunk boundaries (R23/R24).
func readByReads(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	buf := make([]byte, 1<<20)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, string(buf[:n]))
		}
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
}

// readFullSync reads exactly len(want) bytes and compares.
func readFullSync(t *testing.T, r io.Reader, want string) {
	t.Helper()
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("reading %q: %v", want, err)
	}
	if string(buf) != want {
		t.Fatalf("read %q, want %q", buf, want)
	}
}

func loadCassette(t *testing.T, path string) cassetteFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading cassette: %v", err)
	}
	var file cassetteFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parsing cassette: %v", err)
	}
	return file
}

func writeCassette(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handmade.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- round trip -------------------------------------------------------------

// TestCassetteRoundTrip records one chunk-synchronized SSE-ish interaction and
// one plain JSON interaction, then replays: bytes AND chunk boundaries must
// match the recording (R23, R24).
func TestCassetteRoundTrip(t *testing.T) {
	sseChunks := []string{"event: a\ndata: {\"n\":1}\n\n", "event: b\ndata: {\"n\":2}\n\n", "event: c\ndata: {\"n\":3}\n\n"}
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i, c := range sseChunks {
			fmt.Fprint(w, c)
			fl.Flush()
			if i < len(sseChunks)-1 {
				select {
				case <-release: // recorder consumed the chunk
				case <-r.Context().Done():
					return
				}
			}
		}
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"id":"resp-1"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "roundtrip.json")
	rec := Record(path, transport.HTTP())

	sseReq := &transport.Request{
		Method: "POST",
		URL:    srv.URL + "/sse",
		Header: http.Header{
			"Content-Type":  {"application/json"},
			"Authorization": {"Bearer secret-token"},
		},
		Body: []byte(`{"model":"m","stream":true}`),
	}
	resp, err := rec.Do(context.Background(), sseReq)
	if err != nil {
		t.Fatalf("record Do /sse: %v", err)
	}
	for i, c := range sseChunks {
		readFullSync(t, resp.Body, c)
		if i < len(sseChunks)-1 {
			release <- struct{}{}
		}
	}
	if rest, err := io.ReadAll(resp.Body); err != nil || len(rest) != 0 {
		t.Fatalf("trailing sse bytes %q, err %v", rest, err)
	}
	resp.Body.Close()

	jsonReq := &transport.Request{
		Method: "POST",
		URL:    srv.URL + "/json",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"model":"m","stream":false}`),
	}
	resp2, err := rec.Do(context.Background(), jsonReq)
	if err != nil {
		t.Fatalf("record Do /json: %v", err)
	}
	jsonBody, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(jsonBody) != `{"ok":true,"id":"resp-1"}` {
		t.Fatalf("json body = %q", jsonBody)
	}

	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The written file: 2 interactions, streaming boundaries preserved.
	file := loadCassette(t, path)
	if file.Version != 1 || len(file.Interactions) != 2 {
		t.Fatalf("file = version %d, %d interactions", file.Version, len(file.Interactions))
	}
	recChunks := file.Interactions[0].Response.Chunks
	if len(recChunks) < len(sseChunks) {
		t.Fatalf("recorded %d chunks, want >= %d (one per flushed server chunk)", len(recChunks), len(sseChunks))
	}
	if got := strings.Join(recChunks, ""); got != strings.Join(sseChunks, "") {
		t.Fatalf("recorded sse bytes = %q", got)
	}
	// Chunk-synchronized reads: no recorded chunk may span two server chunks.
	{
		remaining := ""
		idx := 0
		for _, rc := range recChunks {
			if remaining == "" {
				remaining = sseChunks[idx]
				idx++
			}
			if !strings.HasPrefix(remaining, rc) {
				t.Fatalf("recorded chunk %q crosses a server flush boundary", rc)
			}
			remaining = remaining[len(rc):]
		}
	}

	// Replay: same requests, byte-identical chunks AND boundaries.
	rp := replayPath(t, path)
	rresp, err := rp.Do(context.Background(), sseReq)
	if err != nil {
		t.Fatalf("replay Do /sse: %v", err)
	}
	gotReads := readByReads(t, rresp.Body)
	rresp.Body.Close()
	if len(gotReads) != len(recChunks) {
		t.Fatalf("replay delivered %d reads, want %d (one stored chunk per Read)", len(gotReads), len(recChunks))
	}
	for i := range gotReads {
		if gotReads[i] != recChunks[i] {
			t.Fatalf("replay chunk %d = %q, want %q", i, gotReads[i], recChunks[i])
		}
	}
	if rresp.Status != 200 {
		t.Errorf("replay status = %d", rresp.Status)
	}
	if ct := rresp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("replay Content-Type = %q", ct)
	}

	rresp2, err := rp.Do(context.Background(), jsonReq)
	if err != nil {
		t.Fatalf("replay Do /json: %v", err)
	}
	replayJSON, _ := io.ReadAll(rresp2.Body)
	rresp2.Body.Close()
	if string(replayJSON) != string(jsonBody) {
		t.Fatalf("replay json body = %q, want %q", replayJSON, jsonBody)
	}
	// Both interactions consumed: the t.Cleanup registered by replayPath will
	// verify on test end.
}

// TestCassetteRedaction (R23): secrets in auth headers, cookies, and the key
// query parameter never reach disk.
func TestCassetteRedaction(t *testing.T) {
	const secret = "sekret-token-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session="+secret)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "redact.json")
	rec := Record(path, transport.HTTP())
	req := &transport.Request{
		Method: "POST",
		URL:    srv.URL + "/v1/models:generate?key=" + secret + "&alt=sse",
		Header: http.Header{
			"Content-Type":   {"application/json"},
			"Authorization":  {"Bearer " + secret},
			"X-Api-Key":      {secret},
			"X-Goog-Api-Key": {secret},
			"Cookie":         {"c=" + secret},
			"X-Harmless":     {"kept-value"},
		},
		Body: []byte(`{"prompt":"hello"}`),
	}
	resp, err := rec.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("secret leaked into cassette file:\n%s", data)
	}
	if !bytes.Contains(data, []byte("REDACTED")) {
		t.Error("no REDACTED markers in file")
	}
	if !bytes.Contains(data, []byte("key=REDACTED")) {
		t.Error("query key parameter not rewritten to key=REDACTED")
	}
	if !bytes.Contains(data, []byte("kept-value")) {
		t.Error("non-sensitive header value dropped")
	}
	if !bytes.Contains(data, []byte("alt=sse")) {
		t.Error("non-sensitive query parameter dropped")
	}

	// body_json readable (R23): JSON bodies are stored parsed, not base64.
	file := loadCassette(t, path)
	reqRec := file.Interactions[0].Request
	if len(reqRec.BodyJSON) == 0 || reqRec.BodyB64 != "" {
		t.Fatalf("JSON body stored as body_json=%q body_b64=%q; want parsed JSON", reqRec.BodyJSON, reqRec.BodyB64)
	}
	var parsed map[string]any
	if err := json.Unmarshal(reqRec.BodyJSON, &parsed); err != nil || parsed["prompt"] != "hello" {
		t.Fatalf("body_json = %s (err %v)", reqRec.BodyJSON, err)
	}
	if !bytes.Contains(data, []byte(`"prompt"`)) {
		t.Error("body_json not human-readable in the file bytes")
	}
}

// TestCassetteChunksB64 (R23): responses that are not valid UTF-8 are stored
// as chunks_b64 and replay byte-identically.
func TestCassetteChunksB64(t *testing.T) {
	binary := []byte{0xff, 0xfe, 0x00, 0x01, 'a', 0x80}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "binary.json")
	rec := Record(path, transport.HTTP())
	req := &transport.Request{Method: "GET", URL: srv.URL}
	resp, err := rec.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, binary) {
		t.Fatalf("recorded read = %v", got)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	file := loadCassette(t, path)
	ia := file.Interactions[0].Response
	if len(ia.ChunksB64) == 0 || len(ia.Chunks) != 0 {
		t.Fatalf("binary response stored as chunks=%v chunks_b64=%v; want chunks_b64", ia.Chunks, ia.ChunksB64)
	}

	rp := replayPath(t, path)
	rresp, err := rp.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if !bytes.Equal(replayed, binary) {
		t.Fatalf("replayed = %v, want %v", replayed, binary)
	}
}

// ---- replay failure modes (R24), via fakeT ---------------------------------

const handmadeCassette = `{
  "version": 1,
  "interactions": [
    {
      "request": {
        "method": "POST",
        "url": "http://example.test/v1/x",
        "body_json": {"a": 1, "b": {"c": [1, 2]}}
      },
      "response": {"status": 200, "chunks": ["ok"]}
    }
  ]
}`

func handmadeRequest(body string) *transport.Request {
	return &transport.Request{
		Method: "POST",
		URL:    "http://example.test/v1/x",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(body),
	}
}

func TestCassetteMismatch(t *testing.T) {
	t.Run("wrong body fails with diff", func(t *testing.T) {
		path := writeCassette(t, handmadeCassette)
		ft := newFakeT(t)
		rp := replayPath(ft, path)
		expectFatal(t, ft, func() {
			rp.Do(context.Background(), handmadeRequest(`{"a":1,"b":{"c":[1,3]}}`))
		})
		msg := ft.lastFatal(t)
		if !strings.Contains(msg, "body mismatch") {
			t.Errorf("fatal = %q, want a body mismatch", msg)
		}
		if !strings.Contains(msg, "-") || !strings.Contains(msg, "+") {
			t.Errorf("fatal = %q, want a diff", msg)
		}
	})

	t.Run("wrong URL fails", func(t *testing.T) {
		path := writeCassette(t, handmadeCassette)
		ft := newFakeT(t)
		rp := replayPath(ft, path)
		expectFatal(t, ft, func() {
			req := handmadeRequest(`{"a":1,"b":{"c":[1,2]}}`)
			req.URL = "http://example.test/v1/OTHER"
			rp.Do(context.Background(), req)
		})
		if msg := ft.lastFatal(t); !strings.Contains(msg, "mismatch") {
			t.Errorf("fatal = %q", msg)
		}
	})

	t.Run("wrong method fails", func(t *testing.T) {
		path := writeCassette(t, handmadeCassette)
		ft := newFakeT(t)
		rp := replayPath(ft, path)
		expectFatal(t, ft, func() {
			req := handmadeRequest(`{"a":1,"b":{"c":[1,2]}}`)
			req.Method = "PUT"
			rp.Do(context.Background(), req)
		})
	})

	t.Run("request beyond recorded interactions fails", func(t *testing.T) {
		path := writeCassette(t, handmadeCassette)
		ft := newFakeT(t)
		rp := replayPath(ft, path)
		resp, err := rp.Do(context.Background(), handmadeRequest(`{"b":{"c":[1,2]},"a":1}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		expectFatal(t, ft, func() {
			rp.Do(context.Background(), handmadeRequest(`{"a":1,"b":{"c":[1,2]}}`))
		})
		if msg := ft.lastFatal(t); !strings.Contains(msg, "beyond recorded interactions") {
			t.Errorf("fatal = %q", msg)
		}
	})

	t.Run("missing cassette file fails", func(t *testing.T) {
		ft := newFakeT(t)
		expectFatal(t, ft, func() {
			replayPath(ft, filepath.Join(t.TempDir(), "does-not-exist.json"))
		})
		if msg := ft.lastFatal(t); !strings.Contains(msg, "RECORD=1") {
			t.Errorf("fatal = %q, want re-record hint", msg)
		}
	})
}

// TestCassetteLeftoverInteractions (R24): unconsumed interactions are reported
// at cleanup.
func TestCassetteLeftoverInteractions(t *testing.T) {
	path := writeCassette(t, handmadeCassette)
	ft := newFakeT(t)
	replayPath(ft, path) // consume nothing
	ft.runCleanups()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if len(ft.errs) != 1 || !strings.Contains(ft.errs[0], "0 of 1 interactions consumed") {
		t.Fatalf("cleanup errors = %q, want leftover-interaction report", ft.errs)
	}
}

// TestCassetteJSONKeyOrder (R24): bodies are compared as canonical JSON — map
// key order must not matter.
func TestCassetteJSONKeyOrder(t *testing.T) {
	path := writeCassette(t, handmadeCassette)
	rp := replayPath(t, path)
	// Same JSON, different key order and whitespace.
	resp, err := rp.Do(context.Background(), handmadeRequest(`{ "b": {"c": [1, 2]}, "a": 1 }`))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" || resp.Status != 200 {
		t.Fatalf("replayed = %d %q", resp.Status, body)
	}
}

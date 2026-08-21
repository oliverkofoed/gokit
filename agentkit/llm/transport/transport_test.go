package transport

// Tests for the HTTP transport (SPEC R21, R22) against httptest servers.
// Synchronization is channel-based throughout — no sleeps.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// readFull reads exactly n bytes from r (across as many Reads as needed).
func readFull(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("reading %d bytes: %v", n, err)
	}
	return string(buf)
}

// TestHTTPStreaming (R22): response bytes are observable incrementally. The
// server writes chunk i+1 only after the client has fully consumed chunk i,
// so each chunk must be delivered by its own read — io.ReadAll-style buffering
// would deadlock, and a test timeout would flag it.
func TestHTTPStreaming(t *testing.T) {
	chunks := []string{"chunk-zero;", "chunk-one!!", "chunk-two.."}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i, c := range chunks {
			fmt.Fprint(w, c)
			fl.Flush()
			if i < len(chunks)-1 {
				select {
				case <-release: // client consumed the chunk
				case <-r.Context().Done():
					return
				}
			}
		}
	}))
	defer srv.Close()

	resp, err := HTTP().Do(context.Background(), &Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}

	for i, want := range chunks {
		if got := readFull(t, resp.Body, len(want)); got != want {
			t.Fatalf("chunk %d = %q, want %q", i, got, want)
		}
		if i < len(chunks)-1 {
			release <- struct{}{}
		}
	}
	if rest, err := io.ReadAll(resp.Body); err != nil || len(rest) != 0 {
		t.Fatalf("trailing bytes %q, err %v", rest, err)
	}
}

// TestHTTPNon2xx (R21): error statuses are normal responses, not errors.
func TestHTTPNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":"overloaded"}`)
	}))
	defer srv.Close()

	resp, err := HTTP().Do(context.Background(), &Request{Method: "POST", URL: srv.URL, Body: []byte("{}")})
	if err != nil {
		t.Fatalf("Do returned error for non-2xx: %v (R21: classification is the caller's job)", err)
	}
	defer resp.Body.Close()
	if resp.Status != 429 {
		t.Fatalf("status = %d", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != `{"error":"overloaded"}` {
		t.Fatalf("body = %q, err %v", body, err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestHTTPCtxCancelMidBody: cancelling the request context while the body is
// mid-stream turns subsequent reads into errors.
func TestHTTPCtxCancelMidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first-part")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the response open until the client cancels
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := HTTP().Do(ctx, &Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if got := readFull(t, resp.Body, len("first-part")); got != "first-part" {
		t.Fatalf("first chunk = %q", got)
	}
	cancel()
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("read after cancel succeeded; want error")
	}
	if !errors.Is(err, context.Canceled) {
		// Depending on timing the http client may surface its own wrapper; a
		// non-nil error is the contract, context.Canceled the common case.
		t.Logf("read error after cancel: %v (not context.Canceled, still acceptable)", err)
	}
}

// TestHTTPHeaders: request headers (including multi-valued) reach the server;
// response headers come back.
func TestHTTPHeaders(t *testing.T) {
	var mu sync.Mutex
	var gotValues []string
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotValues = r.Header.Values("X-Req-Token")
		gotMethod = r.Method
		gotBody = string(b)
		mu.Unlock()
		w.Header().Set("X-Resp-Token", "resp-xyz")
		w.Header().Add("X-Multi", "1")
		w.Header().Add("X-Multi", "2")
		fmt.Fprint(w, "done")
	}))
	defer srv.Close()

	h := http.Header{}
	h.Add("X-Req-Token", "abc")
	h.Add("X-Req-Token", "def")
	resp, err := HTTP().Do(context.Background(), &Request{
		Method: "POST", URL: srv.URL, Header: h, Body: []byte(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	mu.Lock()
	defer mu.Unlock()
	if gotMethod != "POST" || gotBody != `{"k":"v"}` {
		t.Errorf("server saw %s %q", gotMethod, gotBody)
	}
	if len(gotValues) != 2 || gotValues[0] != "abc" || gotValues[1] != "def" {
		t.Errorf("server saw X-Req-Token = %v, want [abc def]", gotValues)
	}
	if got := resp.Header.Get("X-Resp-Token"); got != "resp-xyz" {
		t.Errorf("X-Resp-Token = %q", got)
	}
	if got := resp.Header.Values("X-Multi"); len(got) != 2 {
		t.Errorf("X-Multi = %v", got)
	}
}

// TestHTTPBodyReplayable: Request.Body is a byte slice the transport does not
// consume — the same Request can be issued repeatedly (retries, recording).
func TestHTTPBodyReplayable(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	req := &Request{Method: "POST", URL: srv.URL, Body: []byte("replay-me")}
	tr := HTTP()
	for i := 0; i < 2; i++ {
		resp, err := tr.Do(context.Background(), req)
		if err != nil {
			t.Fatalf("Do #%d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0] != "replay-me" || bodies[1] != "replay-me" {
		t.Fatalf("server saw bodies %q, want replay-me twice", bodies)
	}
	if string(req.Body) != "replay-me" {
		t.Fatalf("req.Body mutated to %q", req.Body)
	}
}

// TestHTTPTransportError (R21): failure to obtain a response returns an error.
func TestHTTPTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused from here on

	resp, err := HTTP().Do(context.Background(), &Request{Method: "GET", URL: url})
	if err == nil {
		resp.Body.Close()
		t.Fatal("Do to a closed server succeeded")
	}
}

// TestHTTPWithClient: WithClient substitutes the underlying *http.Client —
// proven with a TLS test server whose certificate only the substituted
// client trusts.
func TestHTTPWithClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure")
	}))
	defer srv.Close()

	if _, err := HTTP().Do(context.Background(), &Request{Method: "GET", URL: srv.URL}); err == nil {
		t.Fatal("default client trusted the test TLS cert; expected x509 failure")
	}

	resp, err := HTTP(WithClient(srv.Client())).Do(context.Background(), &Request{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Do with substituted client: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure" {
		t.Fatalf("body = %q", body)
	}
}

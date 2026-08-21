package llm

// Tests for the client's retry internals (SPEC R12): isRetryableStatus,
// retryAfter, backoff, and doJSON end-to-end through a scripted transport.
// Protocol paths are deliberately not exercised here.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// clienttestResponse scripts one transport result.
type clienttestResponse struct {
	status int
	header http.Header
	body   string
	err    error // if set, Do returns this instead of a response
}

// clienttestTransport serves a scripted response sequence and records every
// request. Unlike captureTransport (wiretest_test.go) it scripts *sequences*,
// which retry tests need.
type clienttestTransport struct {
	mu        sync.Mutex
	responses []clienttestResponse
	reqs      []*transport.Request
	onRequest func(n int) // called with the 0-based request ordinal
}

func (tr *clienttestTransport) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
	tr.mu.Lock()
	cp := *req
	cp.Body = append([]byte(nil), req.Body...)
	tr.reqs = append(tr.reqs, &cp)
	n := len(tr.reqs) - 1
	hook := tr.onRequest
	if n >= len(tr.responses) {
		tr.mu.Unlock()
		return nil, errors.New("clienttest: no scripted response left")
	}
	r := tr.responses[n]
	tr.mu.Unlock()

	if hook != nil {
		hook(n)
	}
	if r.err != nil {
		return nil, r.err
	}
	h := r.header
	if h == nil {
		h = http.Header{}
	}
	return &transport.Response{Status: r.status, Header: h, Body: io.NopCloser(strings.NewReader(r.body))}, nil
}

func (tr *clienttestTransport) requestCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return len(tr.reqs)
}

func retryAfterHeader(v string) http.Header {
	h := http.Header{}
	h.Set("Retry-After", v)
	return h
}

func TestClientIsRetryableStatus(t *testing.T) {
	cases := map[int]bool{
		200: false, 201: false, 204: false,
		400: false, 401: false, 403: false, 404: false, 409: false, 422: false, 499: false,
		408: true, 429: true,
		500: true, 502: true, 503: true, 504: true, 599: true,
	}
	for status, want := range cases {
		if got := isRetryableStatus(status); got != want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestClientRetryAfter(t *testing.T) {
	t.Run("seconds", func(t *testing.T) {
		d, ok := retryAfter(retryAfterHeader("3"))
		if !ok || d != 3*time.Second {
			t.Fatalf("= %v, %v; want 3s, true", d, ok)
		}
	})
	t.Run("zero seconds", func(t *testing.T) {
		d, ok := retryAfter(retryAfterHeader("0"))
		if !ok || d != 0 {
			t.Fatalf("= %v, %v; want 0, true", d, ok)
		}
	})
	t.Run("seconds capped at 60s", func(t *testing.T) {
		d, ok := retryAfter(retryAfterHeader("120"))
		if !ok || d != 60*time.Second {
			t.Fatalf("= %v, %v; want 60s cap, true", d, ok)
		}
	})
	t.Run("HTTP date", func(t *testing.T) {
		v := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
		d, ok := retryAfter(retryAfterHeader(v))
		if !ok || d <= 0 || d > 10*time.Second {
			t.Fatalf("= %v, %v; want (0s, 10s], true", d, ok)
		}
	})
	t.Run("HTTP date capped at 60s", func(t *testing.T) {
		v := time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)
		d, ok := retryAfter(retryAfterHeader(v))
		if !ok || d != 60*time.Second {
			t.Fatalf("= %v, %v; want 60s cap, true", d, ok)
		}
	})
	t.Run("HTTP date in the past clamps to zero", func(t *testing.T) {
		v := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
		d, ok := retryAfter(retryAfterHeader(v))
		if !ok || d != 0 {
			t.Fatalf("= %v, %v; want 0, true", d, ok)
		}
	})
	t.Run("absent", func(t *testing.T) {
		if d, ok := retryAfter(http.Header{}); ok || d != 0 {
			t.Fatalf("= %v, %v; want 0, false", d, ok)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		if _, ok := retryAfter(retryAfterHeader("soon")); ok {
			t.Fatal("garbage value parsed")
		}
	})
	t.Run("negative", func(t *testing.T) {
		if _, ok := retryAfter(retryAfterHeader("-5")); ok {
			t.Fatal("negative seconds parsed")
		}
	})
}

// TestClientBackoff: 500ms·2^n growth with a 30s cap and up to 25% jitter.
// Jitter forbids exact-value assertions; assert the [base, 1.25·base] range
// and that ranges grow monotonically until the cap.
func TestClientBackoff(t *testing.T) {
	base := func(attempt int) time.Duration {
		d := 500 * time.Millisecond << attempt
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		return d
	}
	for attempt := 0; attempt <= 8; attempt++ {
		lo := base(attempt)
		hi := lo + lo/4
		for i := 0; i < 25; i++ {
			d := backoff(attempt)
			if d < lo || d > hi {
				t.Fatalf("backoff(%d) = %v outside [%v, %v]", attempt, d, lo, hi)
			}
		}
		// Monotone growth: below the cap, even max jitter at n stays under the
		// next attempt's minimum.
		if next := base(attempt + 1); next < 30*time.Second && hi >= next {
			t.Fatalf("attempt %d: max %v not below next base %v", attempt, hi, next)
		}
	}
	if base(10) != 30*time.Second {
		t.Fatal("cap sanity check")
	}
}

func TestClientDoJSONRetry429ThenSuccess(t *testing.T) {
	tr := &clienttestTransport{responses: []clienttestResponse{
		{status: 429, header: retryAfterHeader("0"), body: "rate limited"},
		{status: 200, body: "ok"},
	}}
	c := New(WithTransport(tr))

	resp, err := c.doJSON(context.Background(), "POST", "http://example.test/v1", nil, map[string]any{"model": "m"})
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	defer resp.Body.Close()
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if n := tr.requestCount(); n != 2 {
		t.Fatalf("requests = %d, want 2 (one retry)", n)
	}
	// Retries are invisible: the request is replayed byte-identically.
	if string(tr.reqs[0].Body) != string(tr.reqs[1].Body) {
		t.Errorf("retried body differs: %q vs %q", tr.reqs[0].Body, tr.reqs[1].Body)
	}
	if ct := tr.reqs[0].Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if string(tr.reqs[0].Body) != `{"model":"m"}` {
		t.Errorf("marshaled body = %q", tr.reqs[0].Body)
	}
}

func TestClientDoJSON400NoRetry(t *testing.T) {
	tr := &clienttestTransport{responses: []clienttestResponse{
		{status: 400, body: `{"error":"bad request body"}`},
		{status: 200, body: "must never be reached"},
	}}
	c := New(WithTransport(tr))

	_, err := c.doJSON(context.Background(), "POST", "http://example.test/v1", nil, map[string]int{"a": 1})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.Status != 400 {
		t.Errorf("status = %d", httpErr.Status)
	}
	if !strings.Contains(httpErr.Body, "bad request body") {
		t.Errorf("body = %q: error body must be preserved verbatim (R8)", httpErr.Body)
	}
	if n := tr.requestCount(); n != 1 {
		t.Fatalf("requests = %d, want 1 (4xx is not retryable)", n)
	}
}

// TestClientDoJSONTransportErrorThenSuccess: connection-level errors are
// retryable. This test incurs one real backoff(0) wait (~0.5–0.65s) because
// the backoff schedule is not injectable; it is not synchronization.
func TestClientDoJSONTransportErrorThenSuccess(t *testing.T) {
	tr := &clienttestTransport{responses: []clienttestResponse{
		{err: errors.New("dial tcp: connection refused")},
		{status: 200, body: "ok"},
	}}
	c := New(WithTransport(tr))

	resp, err := c.doJSON(context.Background(), "POST", "http://example.test/v1", nil, struct{}{})
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	defer resp.Body.Close()
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
	if n := tr.requestCount(); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
}

func TestClientDoJSONMaxRetriesExhausted(t *testing.T) {
	tr := &clienttestTransport{responses: []clienttestResponse{
		{status: 429, header: retryAfterHeader("0"), body: "slow down 1"},
		{status: 429, header: retryAfterHeader("0"), body: "slow down 2"},
		{status: 429, header: retryAfterHeader("0"), body: "slow down 3"},
	}}
	c := New(WithTransport(tr), WithRetry(1))

	_, err := c.doJSON(context.Background(), "POST", "http://example.test/v1", nil, struct{}{})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.Status != 429 || !strings.Contains(httpErr.Body, "slow down 2") {
		t.Errorf("err = %v, want the last attempt's 429 body", err)
	}
	if n := tr.requestCount(); n != 2 {
		t.Fatalf("requests = %d, want 2 (initial + 1 retry)", n)
	}
}

// TestClientDoJSONCtxCancelDuringBackoff: the transport cancels the context
// while serving a retryable response with a long Retry-After; the backoff wait
// must abort immediately with ctx.Err() instead of sleeping.
func TestClientDoJSONCtxCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &clienttestTransport{
		responses: []clienttestResponse{
			{status: 503, header: retryAfterHeader("60"), body: "overloaded"},
			{status: 200, body: "must never be reached"},
		},
	}
	tr.onRequest = func(n int) {
		if n == 0 {
			cancel()
		}
	}
	c := New(WithTransport(tr))

	start := time.Now()
	_, err := c.doJSON(ctx, "POST", "http://example.test/v1", nil, struct{}{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := tr.requestCount(); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("doJSON blocked %v; cancellation must interrupt the backoff wait", elapsed)
	}
}

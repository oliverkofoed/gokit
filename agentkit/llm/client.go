package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// defaultThinkingBudgets maps efforts to token budgets for budget-based
// protocols (Anthropic, Gemini) per R14.
func defaultThinkingBudgets() map[Effort]int {
	return map[Effort]int{EffortLow: 2048, EffortMedium: 8192, EffortHigh: 24576}
}

// Client issues completions against any supported protocol. It is safe for
// concurrent use (R13).
type Client struct {
	transport  transport.Interface
	keys       map[string]string
	keyFunc    func(ctx context.Context, provider string) (string, error)
	maxRetries int
	budgets    map[Effort]int
}

// Option configures a Client.
type Option func(*Client)

// WithTransport replaces the network transport (default: transport.HTTP()).
func WithTransport(t transport.Interface) Option {
	return func(c *Client) { c.transport = t }
}

// WithAPIKey sets a static key for a provider name (Model.Provider). Use
// llm.NoAuth for keyless local servers.
func WithAPIKey(provider, key string) Option {
	return func(c *Client) { c.keys[provider] = key }
}

// WithKeyFunc resolves keys dynamically (vaults, expiring gateway tokens).
// Called once per request; returning ("", nil) means "not found, keep going"
// (R11).
func WithKeyFunc(fn func(ctx context.Context, provider string) (string, error)) Option {
	return func(c *Client) { c.keyFunc = fn }
}

// WithRetry sets max retry attempts for retryable failures (default 2). See
// R12 for what retries.
func WithRetry(maxRetries int) Option {
	return func(c *Client) { c.maxRetries = maxRetries }
}

// WithThinkingBudgets overrides token budgets for Anthropic/Gemini effort
// mapping (R14). Missing efforts keep their defaults.
func WithThinkingBudgets(budgets map[Effort]int) Option {
	return func(c *Client) {
		for e, b := range budgets {
			c.budgets[e] = b
		}
	}
}

// New builds a Client. With no options it uses transport.HTTP() and resolves
// keys from the environment (R11).
func New(opts ...Option) *Client {
	c := &Client{
		transport:  transport.HTTP(),
		keys:       map[string]string{},
		maxRetries: 2,
		budgets:    defaultThinkingBudgets(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Stream issues one completion, streaming events as the consumer pulls. It
// never fails after returning: all failures surface as an EventError and a
// final message with StopError/StopAborted (R8).
func (c *Client) Stream(ctx context.Context, req Request) *Stream {
	return NewStream(req.Model, func(emit func(Event) bool) {
		if !emit(Event{Type: EventStart}) {
			return
		}
		key, err := c.resolveKey(ctx, req)
		if err != nil {
			emit(ErrorEvent(err, Usage{}))
			return
		}
		nreq := req
		nreq.Messages = normalizeMessages(req.Model, req.Messages)
		if nreq.MaxTokens == 0 {
			nreq.MaxTokens = req.Model.MaxOutput
		}

		switch req.Model.API {
		case AnthropicMessages:
			c.streamAnthropic(ctx, nreq, key, emit)
		case OpenAIChat:
			c.streamOpenAIChat(ctx, nreq, key, emit)
		case OpenAIResponses:
			c.streamOpenAIResponses(ctx, nreq, key, emit)
		case GoogleGemini:
			c.streamGemini(ctx, nreq, key, emit)
		default:
			emit(ErrorEvent(fmt.Errorf("llm: unknown API %q for model %q", req.Model.API, req.Model.ID), Usage{}))
		}
	})
}

// Complete is Stream followed by Message.
func (c *Client) Complete(ctx context.Context, req Request) (Message, error) {
	return c.Stream(ctx, req).Message()
}

// thinkingBudget returns the token budget for an effort level (R14).
func (c *Client) thinkingBudget(e Effort) int {
	if b, ok := c.budgets[e]; ok {
		return b
	}
	return defaultThinkingBudgets()[EffortMedium]
}

// doJSON marshals payload, issues it through the transport with retry (R12),
// and returns a response with a 2xx status. Retryable failures — transport
// errors, HTTP 408/429/5xx — are retried with exponential backoff before any
// content has been emitted (which is guaranteed here: content events only
// begin once this returns). Other statuses return *HTTPError with the body
// verbatim (R8). The caller owns closing resp.Body.
func (c *Client) doJSON(ctx context.Context, method, url string, header http.Header, payload any) (*transport.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	treq := &transport.Request{Method: method, URL: url, Header: header, Body: body}

	var lastErr error
	for attempt := 0; ; attempt++ {
		resp, err := c.transport.Do(ctx, treq)
		switch {
		case err != nil:
			lastErr = err
		case resp.Status >= 200 && resp.Status < 300:
			return resp, nil
		case isRetryableStatus(resp.Status):
			b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			lastErr = &HTTPError{Status: resp.Status, Body: string(b)}
			if wait, ok := retryAfter(resp.Header); ok && attempt < c.maxRetries {
				if !sleepCtx(ctx, wait) {
					return nil, ctx.Err()
				}
				continue
			}
		default:
			b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			return nil, &HTTPError{Status: resp.Status, Body: string(b)}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt >= c.maxRetries {
			return nil, lastErr
		}
		if !sleepCtx(ctx, backoff(attempt)) {
			return nil, ctx.Err()
		}
	}
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// backoff is 500ms·2^n plus up to 25% jitter.
func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << attempt
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d + time.Duration(rand.Int64N(int64(d/4)+1))
}

// retryAfter parses a Retry-After header (seconds or HTTP date), capped at
// 60s (R12).
func retryAfter(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	const cap = 60 * time.Second
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, cap), true
	}
	if t, err := http.ParseTime(v); err == nil {
		return min(max(time.Until(t), 0), cap), true
	}
	return 0, false
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

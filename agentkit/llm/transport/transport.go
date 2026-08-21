// Package transport is the single seam through which every byte leaves the
// process. Providers never touch net/http directly; they build a Request and
// hand it to an Interface. Replacing the Interface (cassette replay, logging
// middleware, a corporate proxy client) therefore intercepts all traffic.
package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Request is a fully materialized HTTP request. Body is a byte slice, not a
// reader: requests must be replayable for retries and recordable for
// cassettes.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Response is a streamed HTTP response. Body must be closed by the caller.
type Response struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// Interface is the only path bytes take out of the process.
//
// Do returns an error only for failures to *obtain* a response (dial, TLS,
// context cancellation). Non-2xx statuses are returned as a normal Response —
// classification (retry? error event?) is the caller's job.
type Interface interface {
	Do(ctx context.Context, req *Request) (*Response, error)
}

// HTTPOption configures the production transport.
type HTTPOption func(*httpTransport)

// WithClient substitutes the underlying *http.Client.
func WithClient(c *http.Client) HTTPOption {
	return func(t *httpTransport) { t.client = c }
}

// HTTP returns the production transport backed by http.Client. It has no
// overall timeout (streams are long-lived); it respects HTTP_PROXY/HTTPS_PROXY
// via the default transport's http.ProxyFromEnvironment.
func HTTP(opts ...HTTPOption) Interface {
	t := &httpTransport{client: &http.Client{}}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

type httpTransport struct {
	client *http.Client
}

func (t *httpTransport) Do(ctx context.Context, req *Request) (*Response, error) {
	hreq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hreq.Header.Add(k, v)
		}
	}
	hresp, err := t.client.Do(hreq)
	if err != nil {
		return nil, err
	}
	return &Response{
		Status: hresp.StatusCode,
		Header: hresp.Header,
		Body:   hresp.Body,
	}, nil
}

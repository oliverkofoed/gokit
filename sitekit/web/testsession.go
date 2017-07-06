package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/logkit"
)

// TestSession represents a test session for a single user
type TestSession struct {
	site    *Site
	t       *testing.T
	Cookies string
}

func NewTestSession(t *testing.T, site *Site) *TestSession {
	return &TestSession{
		site:    site,
		t:       t,
		Cookies: "",
	}
}

func (s *TestSession) Get(url string) *TestSessionResponse {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return s.errResponse(err)
	}
	return s.Request(req)
}

func (s *TestSession) PostForm(url string, data url.Values) *TestSessionResponse {
	return s.Post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

func (s *TestSession) Post(url string, bodyType string, body io.Reader) *TestSessionResponse {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return s.errResponse(err)
	}
	req.Header.Set("Content-Type", bodyType)
	return s.Request(req)
}

func (s *TestSession) Request(req *http.Request) *TestSessionResponse {
	if s.site == nil {
		return s.errResponse(errors.New("You can only call TestSession.(Get|Post|PostForm|Request) on test sessions configured with a site."))
	}

	return s.do(req, func(recorder http.ResponseWriter) {
		s.site.ServeHTTP(recorder, req)
	})
}

func (s *TestSession) Action(action Action, req *http.Request) *TestSessionResponse {
	if req == nil {
		req, _ = http.NewRequest("GET", "/", nil)
	}

	return s.do(req, func(recorder http.ResponseWriter) {
		ctx, done := logkit.Operation(req.Context(), "web.request", logkit.String("method", req.Method), logkit.String("method", req.URL.Path))
		defer done()
		action(CreateContext(ctx, s.site, nil, recorder, req, nil))
	})
}
func (s *TestSession) PostAction(url string, data url.Values, action Action) *TestSessionResponse {
	req, _ := http.NewRequest("POST", url, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return s.do(req, func(recorder http.ResponseWriter) {
		ctx, done := logkit.Operation(req.Context(), "web.request", logkit.String("method", req.Method), logkit.String("method", req.URL.Path))
		defer done()
		action(CreateContext(ctx, s.site, nil, recorder, req, nil))
	})
}

func (s *TestSession) do(req *http.Request, act func(recorder http.ResponseWriter)) *TestSessionResponse {
	if s.Cookies != "" {
		req.Header.Add("Cookie", s.Cookies)
	}

	recorder := httptest.NewRecorder()
	act(recorder)

	if setCookie, ok := recorder.HeaderMap["Set-Cookie"]; ok {
		s.Cookies = setCookie[0]
	}

	return &TestSessionResponse{
		session:   s,
		Code:      recorder.Code,
		HeaderMap: recorder.HeaderMap,
		Body:      recorder.Body,
	}
}

func (s *TestSession) errResponse(err error) *TestSessionResponse {
	fail(s.t, err.Error())
	return nil
}

type TestSessionResponse struct {
	session   *TestSession
	Code      int           // the HTTP response code from WriteHeader
	HeaderMap http.Header   // the HTTP response headers
	Body      *bytes.Buffer // if non-nil, the bytes.Buffer to append written data to
	// contains filtered or unexported fields
}

func (r *TestSessionResponse) AssertBodyEquals(comparision string) *TestSessionResponse {
	if r.Body.String() != comparision {
		fail(r.session.t, "Body not equal\n------- VALUE: "+fmt.Sprintf("%v", strings.Replace(r.Body.String(), "\n", "\\n", -1))+"\n---- EXPECTED: "+fmt.Sprintf("%v", strings.Replace(comparision, "\n", "\\n", -1)))
	}
	return r
}

func fail(t *testing.T, message string) {
	stack := strings.Split(string(debug.Stack()), "\n")
	var buffer bytes.Buffer

	buffer.WriteString("======= ERROR: " + message + "\n")
	for i, text := range stack {
		if i > 3 {
			buffer.WriteString(text + "\n")
		}
	}

	fmt.Fprintln(os.Stderr, string(buffer.Bytes()))
	time.Sleep(10 * time.Millisecond)
	t.FailNow()
}

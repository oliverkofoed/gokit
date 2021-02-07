package web

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"runtime/debug"

	"bytes"

	"github.com/julienschmidt/httprouter"
	"github.com/oliverkofoed/gokit/logkit"
)

type Action func(context *Context)

type Middleware func(next Action) Action

type TemplateDataWrapper func(c *Context, data interface{}) (interface{}, error)

type Site struct {
	Development           bool
	DefaultMasterFile     string
	TemplateDataWrapper   TemplateDataWrapper
	Assets                Assets
	NotFound              Route
	ServerError           Route
	PanicHandler          func(c *Context, err interface{}) bool
	router                httprouter.Router
	RedirectTrailingSlash bool
	middlewareChain       Action
	BufferedEventsFilter  logkit.BufferedEventsFilter
}

func NewSite(development bool, assetPath string) *Site {
	site := Site{
		Development: development,
	}
	site.router.RedirectTrailingSlash = true
	site.router.RedirectFixedPath = true
	site.Assets = NewAssets(assetPath)
	site.AddRoute(Route{Path: assetPath + ":checksum", NoGZip: true, Action: func(c *Context) {
		site.Assets.Serve(c.Request.URL.Path, c.w, c.Request)
	}})

	site.middlewareChain = func(c *Context) {
		switch {
		case c.Route.Handler != nil:
			c.Route.Handler.ServeHTTP(c.w, c.Request)
		case c.Route.HttpRouterHandle != nil:
			c.Route.HttpRouterHandle(c.w, c.Request, c.params)
		default:
			c.Route.Action(c)
		}
	}

	return &site
}

func (s *Site) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s)
}

type Route struct {
	Path             string
	Action           Action
	Handler          http.Handler
	HttpRouterHandle httprouter.Handle
	Template         string
	MasterTemplate   string
	NoGZip           bool
}

type compressorResponseWriter struct {
	statusCode     int
	hasContentType bool
	io.Writer
	http.ResponseWriter
}

func (w *compressorResponseWriter) WriteHeader(code int) {
	w.statusCode = code
}

func (w *compressorResponseWriter) Write(b []byte) (int, error) {
	if !w.hasContentType {
		if w.Header().Get("Content-Type") == "" {
			contentType := http.DetectContentType(b)
			w.Header().Set("Content-Type", contentType)
		}
		w.hasContentType = true
		if w.statusCode == 0 {
			w.statusCode = 200
		}
		w.ResponseWriter.WriteHeader(w.statusCode)
	}
	return w.Writer.Write(b)
}

func (s *Site) AddMiddleware(middleware Middleware) {
	s.middlewareChain = middleware(s.middlewareChain)
}

func (s *Site) AddRoute(route Route) {
	if route.Action == nil && route.Handler == nil && route.HttpRouterHandle == nil {
		panic("AddRoute expects a Route with either Action, Handler or HttpRouterHandle defined")
	}

	s.router.Handle("GET", route.Path, func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		s.runRoute(&route, w, req, params, false)
	})
}

func (s *Site) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	path := req.URL.Path
	handle, params, trailingSlashRedirect := s.router.Lookup("GET", path)

	if handle != nil {
		handle(w, req, params)
		return
	}

	if req.Method != "CONNECT" && path != "/" {
		code := 301 // Permanent redirect, request with GET method
		if req.Method != "GET" {
			// Temporary redirect, request with same method. As of Go 1.3, Go does not support status code 308.
			code = 307
		}

		if trailingSlashRedirect && s.RedirectTrailingSlash {
			if path[len(path)-1] == '/' {
				req.URL.Path = path[:len(path)-1]
			} else {
				req.URL.Path = path + "/"
			}
			http.Redirect(w, req, req.URL.String(), code)
			return
		}
	}

	// Handle 404
	if s.NotFound.Action != nil {
		s.runRoute(&s.NotFound, w, req, params, false)
	} else {
		http.NotFound(w, req)
	}
}

func RemoveAssetsFilter(events []logkit.Event) []logkit.Event {
	op := events[0]
	if len(op.Fields) > 0 && op.Fields[0].Key == "url" {
		url := op.Fields[0].Str
		if strings.HasPrefix(url, "/_/") {
			return nil
		}
	}
	return events
}

func (s *Site) runRoute(route *Route, w http.ResponseWriter, req *http.Request, params httprouter.Params, dontWrap bool) {
	// automatic zipping of all data.
	if !route.NoGZip && dontWrap == false && req.Method == "GET" {
		if strings.Contains(req.Header.Get("Accept-Encoding"), "gzip") {
			compressor := gzip.NewWriter(w)
			defer compressor.Close()
			w.Header().Set("Content-Encoding", "gzip")
			w = &compressorResponseWriter{Writer: compressor, ResponseWriter: w, hasContentType: false}
		}
	}
	var ctx *logkit.Context
	var done func()
	if s.BufferedEventsFilter != nil {
		ctx, done = logkit.OperationWithOutput(req.Context(), "web.request", logkit.NewBufferedOutput(logkit.DefaultOutput, s.BufferedEventsFilter), logkit.String("url", req.URL.Path), logkit.String("method", req.Method))
	} else {
		ctx, done = logkit.Operation(req.Context(), "web.request", logkit.String("url", req.URL.Path), logkit.String("method", req.Method))
	}
	defer done()

	// create context
	c := CreateContext(ctx, s, route, w, req, params)

	// catch panics
	defer func() {
		if err := recover(); err != nil {
			if s.PanicHandler != nil {
				if s.PanicHandler(c, err) {
					return
				}
			}
			logkit.Error(ctx, fmt.Sprintf("%v", err), logkit.String("stack", stackToPanic(debug.Stack(), 4)))
			if s.ServerError.Action != nil {
				w.WriteHeader(500)
				s.runRoute(&s.ServerError, w, req, make(httprouter.Params, 0, 0), true)
			} else {
				http.Error(w, fmt.Sprintf("%v", err), 500)
			}
		}
	}()

	// run the middleware chain.
	s.middlewareChain(c)
}

func stackToPanic(stack []byte, skipframes int) string {
	if ix := bytes.Index(stack, []byte("runtime/panic.go")); ix != -1 {
		for skipframes > 0 && ix > 0 {
			stack = stack[ix:]
			ix = bytes.Index(stack, []byte("\n"))
			skipframes--
		}
	}
	return string(stack)
}

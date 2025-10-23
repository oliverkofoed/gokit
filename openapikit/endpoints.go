package openapikit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type ApiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Debug   string `json:"debug,omitempty"`
}

func (e ApiError) Error() string {
	return fmt.Sprintf("%v: %v\n%v", e.Code, e.Message, e.Debug)
}

type Method struct {
	Path        string
	Description string
	Service     string
	Name        string
	Action      WrappedAction
}

type ApiMethods struct {
	endpoints   []Method
	schemaCache *OpenAPISchema
}

type WrappedAction struct {
	Name       string
	MakeAction func(development bool) func(*web.Context)
	ArgsType   reflect.Type
	ResultType reflect.Type
}

// New creates a new API endpoints manager
func New() *ApiMethods {
	return &ApiMethods{
		endpoints: make([]Method, 0),
	}
}

// Add registers an API method using the Method struct
func (e *ApiMethods) Add(endpoint Method) {
	e.endpoints = append(e.endpoints, endpoint)
	e.schemaCache = nil
}

// InstallInto installs the API methods into the site
func (e *ApiMethods) InstallInto(site *web.Site, development bool) {
	for _, endpoint := range e.endpoints {
		site.AddRoute(web.Route{
			Path:   endpoint.Path,
			Action: endpoint.Action.MakeAction(development),
			NoGZip: true,
		})
		e.Add(endpoint)
	}
}

// Action wraps a handler function for use in Method
func Action[TArgs any, TResult any](handler func(c *web.Context, args TArgs) (*TResult, error)) WrappedAction {
	// Extract function name for operationId
	funcName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	// Extract just the function name from the full path (e.g., "github.com/user/project/auth.Signup" -> "Signup")
	if idx := strings.LastIndex(funcName, "."); idx >= 0 {
		funcName = funcName[idx+1:]
	}

	return WrappedAction{
		Name:       funcName,
		ArgsType:   reflect.TypeOf((*TArgs)(nil)).Elem(),
		ResultType: reflect.TypeOf((*TResult)(nil)).Elem(),
		MakeAction: func(development bool) func(c *web.Context) {
			return func(c *web.Context) {
				// --- Panic recovery with stack trace ---
				defer func() {
					if r := recover(); r != nil {
						stack := string(debug.Stack())
						writeError(development, c, http.StatusInternalServerError, ApiError{
							Code:    "unhandlederror",
							Message: "An unexpected error occurred",
							Debug:   fmt.Sprintf("panic: %v\n%s", r, stack),
						})
					}
				}()

				switch c.Request.Method {
				case http.MethodPost:
					// ok
				case http.MethodOptions:
					c.Header().Set("Allow", http.MethodPost+", "+http.MethodOptions)
					c.WriteHeader(http.StatusNoContent)
					return
				default:
					c.Header().Set("Allow", http.MethodPost+", "+http.MethodOptions)
					writeError(development, c, http.StatusMethodNotAllowed, ApiError{
						Code:    "method_not_allowed",
						Message: "Only POST is allowed",
					})
					return
				}

				// Validate Content-Type is JSON (accept +json)
				ct := c.Request.Header.Get("Content-Type")
				if ct == "" {
					writeError(development, c, http.StatusUnsupportedMediaType, ApiError{
						Code:    "unsupported_media_type",
						Message: "Content-Type must be application/json",
					})
					return
				}
				mediaType, _, err := mime.ParseMediaType(ct)
				if err != nil || !(mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) {
					writeError(development, c, http.StatusUnsupportedMediaType, ApiError{
						Code:    "unsupported_media_type",
						Message: "Content-Type must be application/json",
					})
					return
				}

				// Decode with limits and strictness
				var args TArgs
				r := c.Request
				r.Body = http.MaxBytesReader(c, r.Body, 1<<20)
				dec := json.NewDecoder(r.Body)
				dec.DisallowUnknownFields()

				if err := dec.Decode(&args); err != nil {
					var msg string
					switch {
					case errors.Is(err, http.ErrBodyReadAfterClose):
						msg = "Request body closed unexpectedly"
					case errors.Is(err, io.EOF):
						msg = "Request body is empty"
					case strings.Contains(err.Error(), "http: request body too large"):
						writeError(development, c, http.StatusRequestEntityTooLarge, ApiError{
							Code:    "payload_too_large",
							Message: "Request JSON exceeds 1MB",
						})
						return
					default:
						msg = fmt.Sprintf("Invalid JSON: %v", err)
					}
					writeError(development, c, http.StatusBadRequest, ApiError{
						Code:    "invalid_json",
						Message: msg,
					})
					return
				}

				// Detect trailing data (beyond a single top-level value)
				var extra any
				if err := dec.Decode(&extra); err != io.EOF {
					writeError(development, c, http.StatusBadRequest, ApiError{
						Code:    "invalid_json",
						Message: "Trailing data after JSON value",
					})
					return
				}

				// Call the handler
				result, hErr := handler(c, args)
				if hErr != nil {
					var hex ApiError
					if errors.As(hErr, &hex) {
						writeError(development, c, http.StatusBadRequest, hex)
						return
					}
					var he *ApiError
					if errors.As(hErr, &he) {
						writeError(development, c, http.StatusBadRequest, *he)
						return
					}
					writeError(development, c, http.StatusInternalServerError, ApiError{
						Code:    "internal_error",
						Message: "An unexpected error occurred",
						Debug:   fmt.Sprintf("%v", hErr),
					})
					return
				}

				// all good
				c.Header().Set("Content-Type", "application/json; charset=utf-8")
				enc := json.NewEncoder(c)
				enc.SetEscapeHTML(false)
				if development {
					enc.SetIndent("", "  ")
				}
				c.WriteHeader(http.StatusOK)
				_ = enc.Encode(result)
			}
		},
	}
}

func writeError(development bool, c *web.Context, statusCode int, err ApiError) {
	c.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.WriteHeader(statusCode)
	if !development {
		err.Debug = ""
	}
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(err)
}

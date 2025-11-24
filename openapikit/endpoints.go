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

	// Check if TArgs has any io.Reader fields
	hasFile := false
	argsType := reflect.TypeOf((*TArgs)(nil)).Elem()
	if argsType.Kind() == reflect.Struct {
		for i := 0; i < argsType.NumField(); i++ {
			if argsType.Field(i).Type == reflect.TypeOf((*io.Reader)(nil)).Elem() {
				hasFile = true
				break
			}
		}
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

				var args TArgs

				if hasFile {
					// Validate Content-Type is multipart/form-data
					ct := c.Request.Header.Get("Content-Type")
					if !strings.HasPrefix(ct, "multipart/form-data") {
						writeError(development, c, http.StatusUnsupportedMediaType, ApiError{
							Code:    "unsupported_media_type",
							Message: "Content-Type must be multipart/form-data",
						})
						return
					}

					// Parse multipart form
					if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32MB max memory
						writeError(development, c, http.StatusBadRequest, ApiError{
							Code:    "invalid_multipart",
							Message: "Failed to parse multipart form: " + err.Error(),
						})
						return
					}

					// Map form fields to args
					val := reflect.ValueOf(&args).Elem()
					typ := val.Type()

					for i := 0; i < val.NumField(); i++ {
						field := typ.Field(i)
						fieldVal := val.Field(i)

						// Get name from json tag
						name := field.Name
						if tag := field.Tag.Get("json"); tag != "" {
							parts := strings.Split(tag, ",")
							if parts[0] != "" {
								name = parts[0]
							}
						}

						if field.Type == reflect.TypeOf((*io.Reader)(nil)).Elem() {
							file, _, err := c.Request.FormFile(name)
							if err == nil {
								fieldVal.Set(reflect.ValueOf(file))
							}
						} else {
							formVal := c.Request.FormValue(name)
							if formVal != "" {
								// Handle different types
								switch field.Type.Kind() {
								case reflect.String:
									fieldVal.SetString(formVal)
								case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
									// We could use strconv here, but JSON unmarshal is safer/easier for all types
									var v any
									if err := json.Unmarshal([]byte(formVal), &v); err == nil {
										// This might be tricky because Unmarshal unmarshals numbers to float64
										// Let's try direct unmarshal to the field type
										vPtr := reflect.New(field.Type).Interface()
										if err := json.Unmarshal([]byte(formVal), vPtr); err == nil {
											fieldVal.Set(reflect.ValueOf(vPtr).Elem())
										}
									} else {
										// Fallback for plain strings that aren't quoted JSON strings?
										// If it's an int, json.Unmarshal("123", &i) works.
										// If it's a string, json.Unmarshal("foo", &s) fails, needs "\"foo\"".
										// But for multipart/form-data, usually strings are just sent as is.
										// So if unmarshal fails and it's a string, use the value directly?
										// But we already handled String kind above.
										// What about Int? json.Unmarshal("123") works.
									}
								default:
									// Complex types (structs, slices, maps) or primitives
									vPtr := reflect.New(field.Type).Interface()
									if err := json.Unmarshal([]byte(formVal), vPtr); err == nil {
										fieldVal.Set(reflect.ValueOf(vPtr).Elem())
									} else if field.Type.Kind() == reflect.String {
										// If unmarshal failed and it's a string, it might be a raw string
										fieldVal.SetString(formVal)
									}
								}
							}
						}
					}

				} else {
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
						Debug:   hErr.Error(),
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
	} else {
		j, e := json.MarshalIndent(err, "", "  ")
		if e == nil {
			fmt.Println("returning apierror to client: " + string(j))
		}
	}
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(err)
}

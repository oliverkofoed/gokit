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

	jsoniter "github.com/json-iterator/go"
	"github.com/modern-go/reflect2"
	"github.com/oliverkofoed/gokit/sitekit/web"
)

type TypeFormatter interface {
	Type() reflect.Type
	JsonEncoder() jsoniter.ValEncoder
	JsonDecoder() jsoniter.ValDecoder
	UpdateSchema(schema map[string]any)
}

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
	formatters  []TypeFormatter
	jsonConfig  jsoniter.API
}

type WrappedAction struct {
	Name       string
	MakeAction func(e *ApiMethods, development bool) func(*web.Context)
	ArgsType   reflect.Type
	ResultType reflect.Type
}

// New creates a new API endpoints manager
func New() *ApiMethods {
	return &ApiMethods{
		endpoints:  make([]Method, 0),
		jsonConfig: jsoniter.ConfigCompatibleWithStandardLibrary,
	}
}

func (e *ApiMethods) RegisterJsonFormatter(f TypeFormatter) {
	e.formatters = append(e.formatters, f)
	e.schemaCache = nil

	// Rebuild jsonConfig with the new formatter
	config := jsoniter.ConfigCompatibleWithStandardLibrary
	extension := &jsonFormatterExtension{formatter: f}
	config.RegisterExtension(extension)
	e.jsonConfig = config
}

// readerType is the marker that makes an endpoint multipart: an args field of
// this type is a file upload rather than a value.
var readerType = reflect.TypeOf((*io.Reader)(nil)).Elem()

// jsonFieldName is the name a field answers to on the wire — its json tag, or
// its own name when it has none.
func jsonFieldName(field reflect.StructField) string {
	if tag := field.Tag.Get("json"); tag != "" {
		if name := strings.Split(tag, ",")[0]; name != "" {
			return name
		}
	}
	return field.Name
}

// setFieldFromForm decodes one multipart form value into one args field.
//
// A form value is a bare string — `1f4`, `renamed`, `1786350550` — while every
// other decode path in this package speaks json, registered formatters
// included. So the value is offered to the json decoder twice: as it arrived,
// and as a json string literal. Whichever parses first wins, and which one is
// tried first is decided by the field rather than by the text:
//
//   - For a string field the text *is* the value, so the quoted form leads.
//     Raw-first would read `123` as a number, `null` as nothing at all and
//     `"quoted"` as `quoted` — three ways to quietly rewrite what was typed.
//   - Everything else is a json literal first and a string second. That second
//     attempt is what carries formatters whose wire shape is a string:
//     Int64HexFormatter writes `"1f4"`, and no form on earth sends the quotes.
//     Without it such a field stays zero, and a zero id reads downstream as
//     "no such row" rather than as "never parsed".
//
// Both attempts go through the configured json, so a type with a registered
// codec is decoded by that codec — which assigning a string directly, as this
// used to do, silently skipped.
func setFieldFromForm(cfg jsoniter.API, fieldType reflect.Type, fieldVal reflect.Value, formVal string) {
	// Marshalling a string cannot fail; the error is checked rather than
	// ignored so that a config which somehow does fail leaves the raw attempt
	// standing instead of feeding the decoder an empty document.
	quoted, err := cfg.Marshal(formVal)
	if err != nil {
		quoted = nil
	}

	attempts := [][]byte{[]byte(formVal), quoted}
	if fieldType.Kind() == reflect.String {
		attempts = [][]byte{quoted, []byte(formVal)}
	}

	for _, attempt := range attempts {
		if len(attempt) == 0 {
			continue
		}
		ptr := reflect.New(fieldType)
		if err := cfg.Unmarshal(attempt, ptr.Interface()); err == nil {
			fieldVal.Set(ptr.Elem())
			return
		}
	}

	// Neither shape parsed: the field keeps its zero value. A codec that
	// rejects its input is answering the question, and this is its answer.
}

type jsonFormatterExtension struct {
	jsoniter.DummyExtension
	formatter TypeFormatter
}

func (e *jsonFormatterExtension) CreateEncoder(typ reflect2.Type) jsoniter.ValEncoder {
	if typ.Type1() == e.formatter.Type() {
		return e.formatter.JsonEncoder()
	}
	return nil
}

func (e *jsonFormatterExtension) CreateDecoder(typ reflect2.Type) jsoniter.ValDecoder {
	if typ.Type1() == e.formatter.Type() {
		return e.formatter.JsonDecoder()
	}
	return nil
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
			Action: endpoint.Action.MakeAction(e, development),
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
			if argsType.Field(i).Type == readerType {
				hasFile = true
				break
			}
		}
	}

	return WrappedAction{
		Name:       funcName,
		ArgsType:   reflect.TypeOf((*TArgs)(nil)).Elem(),
		ResultType: reflect.TypeOf((*TResult)(nil)).Elem(),
		MakeAction: func(e *ApiMethods, development bool) func(c *web.Context) {
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
						name := jsonFieldName(field)

						if field.Type == readerType {
							if file, _, err := c.Request.FormFile(name); err == nil {
								fieldVal.Set(reflect.ValueOf(file))
							}
							continue
						}

						// An absent field and an empty one are the same thing here:
						// the zero value, which is what the field already holds.
						if formVal := c.Request.FormValue(name); formVal != "" {
							setFieldFromForm(e.jsonConfig, field.Type, fieldVal, formVal)
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
					dec := e.jsonConfig.NewDecoder(r.Body)
					// dec.DisallowUnknownFields() // json-iterator doesn't have this on the decoder exactly the same way, but let's see

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
				enc := e.jsonConfig.NewEncoder(c)
				if development {
					// json-iterator doesn't have SetEscapeHTML(false) exactly like encoding/json,
					// but it defaults to escaping.
					// enc.SetEscapeHTML(false)
					// enc.SetIndent("", "  ")
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

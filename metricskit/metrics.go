package metricskit

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/oliverkofoed/gokit/logkit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var apiTotalRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "api_requests_total",
		Help: "Number of api requests.",
	},
	[]string{"path"},
)

var apiResponseStatus = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "api_response_status",
		Help: "Status of api response",
	},
	[]string{"status"},
)

var apiDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "api_response_time_seconds",
	Help: "Duration of API requests.",
}, []string{"path"})

var httpTotalRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Number of get requests.",
	},
	[]string{"path"},
)

var httpResponseStatus = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_response_status",
		Help: "Status of HTTP response",
	},
	[]string{"status"},
)

var httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "http_response_time_seconds",
	Help: "Duration of HTTP requests.",
}, []string{"path"})

var sqlDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "sql_duration_milliseconds",
	Help: "Duration of sql requests.",
}, []string{"sql"})

var output = NewWriterOutput(os.Stdout, true)

func init() {
	prometheus.Register(apiTotalRequests)
	prometheus.Register(apiResponseStatus)
	prometheus.Register(apiDuration)
	prometheus.Register(httpTotalRequests)
	prometheus.Register(httpResponseStatus)
	prometheus.Register(httpDuration)
	prometheus.Register(sqlDuration)
}

func Handler(metricsPath string, basicHttpAuthenticator func(r *http.Request, username string, password string) (bool, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == metricsPath {
			if basicHttpAuthenticator != nil {
				username, password, _ := r.BasicAuth()
				allow, realm := basicHttpAuthenticator(r, username, password)
				if !allow {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=\"%v\"", realm))
					w.WriteHeader(401)
					w.Write([]byte("Unauthorised.\n"))
					return
				}

			}
			promhttp.Handler().ServeHTTP(w, r)
			return
		}
	})
}

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		timer := prometheus.NewTimer(httpDuration.WithLabelValues(path))
		rw := NewResponseWriter(w)

		var duration time.Duration
		var statusCode int
		ctx, done := logkit.OperationWithOutput(r.Context(), "http.request", logkit.NewBufferedOutput(output, func(e []logkit.Event) []logkit.Event {
			if duration > time.Millisecond*20 || (statusCode != 200 && statusCode != 404 && statusCode != 301 && statusCode != 302) {
				if len(e) > 0 {
					first := e[0]
					if first.Type == logkit.EventTypeBeginOperation && first.Operation != nil && first.Operation.Name == "http.request" {
						first.Operation.Fields = append(first.Operation.Fields, logkit.Int("statuscode", statusCode))
					}
				}
				fmt.Println("")
				return e
			}
			return nil
		}), logkit.String("url", path), logkit.String("method", r.Method))
		defer done()

		r = r.WithContext(ctx)
		next.ServeHTTP(rw, r)
		statusCode = rw.statusCode

		duration = timer.ObserveDuration()
		httpResponseStatus.WithLabelValues(strconv.Itoa(statusCode)).Inc()
		httpTotalRequests.WithLabelValues(path).Inc()
	})
}

// ----------

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func NewResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return rw.ResponseWriter.(http.Hijacker).Hijack()
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ----------

const maxStringPrintLength = 30

var (
	termReset   = []byte("\033[0;5;0m")
	termBold    = []byte("\033[1m")
	termNotBold = []byte("\033[0m")
	termRed     = []byte("\033[31;1m")
	termYellow  = []byte("\033[33m")
	termGray    = []byte("\033[90m")
)

type WriterOutput struct {
	sync.RWMutex
	output io.Writer
	colors bool
}

func NewWriterOutput(output io.Writer, terminalColors bool) logkit.Output {
	return &WriterOutput{output: output, colors: terminalColors}
}

var inQueryRegex = regexp.MustCompile("in\\W+\\([^)]*\\)")

func (d *WriterOutput) Event(evt logkit.Event) {
	switch evt.Type {
	case logkit.EventTypeBeginOperation:
		d.Lock()
		defer d.Unlock()
		d.writePrefix(evt.Operation)

		t := evt.Operation.End.Sub(evt.Operation.Start)
		io.WriteString(d.output, "(")
		io.WriteString(d.output, t.String())
		io.WriteString(d.output, ")")

		if evt.Operation.Name == "pg.sql" {
			for _, field := range evt.Operation.Fields {
				if field.FieldType == logkit.FieldTypeString && field.Key == "sql" {
					sql := field.Str
					sql = inQueryRegex.ReplaceAllString(sql, "in (...)")
					sqlDuration.WithLabelValues(sql).Observe(float64(t.Milliseconds()))
				}
			}
		}

		logkit.PrintValues(d.output, evt.Operation.Fields)
		io.WriteString(d.output, "\n")
	case logkit.EventTypeCompleteOperation:
	default:
		d.Lock()
		defer d.Unlock()
		if d.colors {
			colorOutput(d.output, evt.Type)
		}
		d.writePrefix(evt.Operation)
		if d.colors {
			d.output.Write(termReset)
			colorOutput(d.output, evt.Type)
		}
		if evt.Operation.Parent != nil {
			io.WriteString(d.output, ": ")
		}
		io.WriteString(d.output, evt.Message)
		if d.colors {
			d.output.Write(termReset)
		}
		logkit.PrintValues(d.output, evt.Fields)
		io.WriteString(d.output, "\n")
	}
}

func (d *WriterOutput) writePrefix(operation *logkit.Context) {
	if d.colors {
		d.output.Write(termBold)
		d.writePath(operation)
		d.output.Write(termNotBold)
	} else {
		d.writePath(operation)
	}
}

func (d *WriterOutput) writePath(operation *logkit.Context) {
	if operation.Parent != nil && operation.Parent.Name != "" {
		d.writePath(operation.Parent)
		io.WriteString(d.output, "→")
	}
	io.WriteString(d.output, operation.Name)
}

func colorOutput(w io.Writer, t logkit.EventType) {
	switch t {
	case logkit.EventTypeDebug:
		w.Write(termGray)
	case logkit.EventTypeInfo:
		//w.Write(termGray)
	case logkit.EventTypeWarn:
		w.Write(termYellow)
	case logkit.EventTypeError:
		w.Write(termRed)
	}
}

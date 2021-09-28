package longjobkit

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/oliverkofoed/gokit/logkit"
)

type threadSafeWriter struct {
	sync.RWMutex
	w io.WriteCloser
}

func (w *threadSafeWriter) Write(p []byte) (n int, err error) {
	w.Lock()
	defer w.Unlock()
	return w.w.Write(p)
}

func (w *threadSafeWriter) Close() error {
	w.Lock()
	defer w.Unlock()
	return w.w.Close()
}

type Result struct {
	Hostname string
	SaveLog  bool
	AnyError bool
	Log      *bytes.Buffer
}

func Run(ctx context.Context, name string, action func(ctx context.Context) (bool, error)) *Result {
	result := &Result{
		Log: bytes.NewBuffer(nil),
	}
	if hostname, err := os.Hostname(); err == nil {
		result.Hostname = hostname
	} else {
		result.Hostname = fmt.Sprintf("error: %v", err.Error())
	}

	zipper := &threadSafeWriter{w: gzip.NewWriter(result.Log)}
	errMarker := &errorMarker{}

	scheduleCtx, done := logkit.OperationWithOutput(ctx, name, logkit.NewSplitterOutput(errMarker, logkit.DefaultOutput, logkit.NewWriterOutput(zipper, true)))

	start := time.Now()
	logkit.Info(scheduleCtx, "starting "+name)
	defer func() {
		if err := recover(); err != nil {
			if asErr, ok := err.(error); ok {
				logkit.Error(scheduleCtx, "unhandled panic", logkit.Err(asErr), logkit.String("Stack", string(debug.Stack())))
				errMarker.AnyError = true
			} else {
				logkit.Error(scheduleCtx, "unhandled panic", logkit.Interface("err", err), logkit.String("Stack", string(debug.Stack())))
				errMarker.AnyError = true
			}
		}
		time.Sleep(time.Second)
		logkit.Info(scheduleCtx, "done", logkit.Duration("duration", time.Since(start)))

		done()

		zipper.Close()
		if errMarker.AnyError {
			result.AnyError = true
		}
	}()

	// do the schedule
	var err error
	result.SaveLog, err = action(scheduleCtx)
	if err != nil {
		logkit.Error(scheduleCtx, "returned error", logkit.Err(err))
		errMarker.AnyError = true
	}

	return result
}

type errorMarker struct {
	AnyError bool
}

func (e *errorMarker) Event(evt logkit.Event) {
	if evt.Type == logkit.EventTypeError || evt.Type == logkit.EventTypeWarn {
		e.AnyError = true
	}
}

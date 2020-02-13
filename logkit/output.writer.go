package logkit

import (
	"io"
	"sync"
	"time"
)

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

func NewWriterOutput(output io.Writer, terminalColors bool) Output {
	return &WriterOutput{output: output, colors: terminalColors}
}

func (d *WriterOutput) Event(evt Event) {
	switch evt.Type {
	case EventTypeBeginOperation:
		d.Lock()
		defer d.Unlock()
		d.writePrefix(evt.Operation)
		PrintValues(d.output, evt.Operation.Fields)
		io.WriteString(d.output, "\n")
	case EventTypeCompleteOperation:
		t := evt.Operation.End.Sub(evt.Operation.Start)
		if t > time.Millisecond*20 {
			d.writePrefix(evt.Operation)
			d.Lock()
			defer d.Unlock()
			io.WriteString(d.output, " finished in ")
			io.WriteString(d.output, t.String())
			io.WriteString(d.output, "\n")
		}
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
		PrintValues(d.output, evt.Fields)
		io.WriteString(d.output, "\n")
	}
}

func (d *WriterOutput) writePrefix(operation *Context) {
	if d.colors {
		d.output.Write(termBold)
		d.writePath(operation)
		d.output.Write(termNotBold)
	} else {
		d.writePath(operation)
	}
}

func (d *WriterOutput) writePath(operation *Context) {
	if operation.Parent != nil && operation.Parent.Name != "" {
		d.writePath(operation.Parent)
		io.WriteString(d.output, "→")
	}
	io.WriteString(d.output, operation.Name)
}

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

type ConsoleOutput struct {
	sync.RWMutex
	output io.Writer
	colors bool
}

func (d *ConsoleOutput) Event(evt Event) {
	switch evt.Type {
	case EventTypeBeginOperation:
		d.Lock()
		defer d.Unlock()
		d.writePrefix(evt.Operation)
		printValues(d.output, evt.Operation.fields)
		io.WriteString(d.output, "\n")
	case EventTypeCompleteOperation:
		t := evt.Operation.end.Sub(evt.Operation.start)
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
		if evt.Operation.parent != nil {
			io.WriteString(d.output, ": ")
		}
		io.WriteString(d.output, evt.Message)
		if d.colors {
			d.output.Write(termReset)
		}
		printValues(d.output, evt.Fields)
		io.WriteString(d.output, "\n")
	}
}

func (d *ConsoleOutput) writePrefix(operation *Context) {
	if d.colors {
		d.output.Write(termBold)
		d.writePath(operation)
		d.output.Write(termNotBold)
	} else {
		d.writePath(operation)
	}
}

func (d *ConsoleOutput) writePath(operation *Context) {
	if operation.parent != nil && operation.parent.name != "" {
		d.writePath(operation.parent)
		io.WriteString(d.output, "→")
	}
	io.WriteString(d.output, operation.name)
}

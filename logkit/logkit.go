package logkit

import (
	"context"
	"os"
	"time"

	isatty "github.com/mattn/go-isatty"
)

// Context implements context.Context and adds some convinience logging methods like ctx.Info("Msg")
type Context struct {
	context.Context
	fields []Field
	output Output
	parent *Context
	name   string
	start  time.Time
	end    time.Time
}

func (c *Context) event(e Event) Event {
	if c.output != nil {
		c.output.Event(e)
	} else {
		DefaultOutput.Event(e)
	}
	return e
}

func (c *Context) Debug(msg string, fields ...Field) error {
	return c.event(Event{Operation: c, Message: msg, Type: EventTypeDebug, Fields: fields})
}

func (c *Context) Info(msg string, fields ...Field) error {
	return c.event(Event{Operation: c, Message: msg, Type: EventTypeInfo, Fields: fields})
}

func (c *Context) Warn(msg string, fields ...Field) error {
	return c.event(Event{Operation: c, Message: msg, Type: EventTypeWarn, Fields: fields})
}

func (c *Context) Error(msg string, fields ...Field) error {
	return c.event(Event{Operation: c, Message: msg, Type: EventTypeError, Fields: fields})
}

func Debug(ctx context.Context, msg string, args ...Field) error {
	return findContext(ctx).Debug(msg, args...)
}

func Info(ctx context.Context, msg string, args ...Field) error {
	return findContext(ctx).Info(msg, args...)
}

func Warn(ctx context.Context, msg string, args ...Field) error {
	return findContext(ctx).Warn(msg, args...)
}

func Error(ctx context.Context, msg string, args ...Field) error {
	return findContext(ctx).Error(msg, args...)
}

// DefaultOuput is the default Output for all logging
var DefaultOutput Output = &ConsoleOutput{
	output: os.Stdout,
	colors: isatty.IsTerminal(os.Stdout.Fd()),
}

var defaultOperation = &Context{
	name:   "",
	parent: nil,
	output: nil,
}

type operationValueKeyType byte

var operationValueKey = operationValueKeyType(0)

func findContext(ctx context.Context) *Context {
	if ctx == nil {
		return defaultOperation
	} else if op, ok := ctx.(*Context); ok {
		return op
	} else if v := ctx.Value(operationValueKey); v != nil {
		return v.(*Context)
	}
	return defaultOperation
}

// --------- package level convinience methods

func OperationWithOutput(ctx context.Context, name string, output Output, fields ...Field) (*Context, func()) {
	return operation(ctx, name, output, fields...)
}

func Operation(ctx context.Context, name string, fields ...Field) (*Context, func()) {
	return operation(ctx, name, nil, fields...)
}

func operation(ctx context.Context, name string, newOutput Output, fields ...Field) (*Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}

	parent := findContext(ctx)

	c := &Context{
		parent: parent,
		output: parent.output,
		name:   name,
		fields: fields,
		start:  time.Now(),
	}
	if newOutput != nil {
		c.output = newOutput
	}

	childContext, done := context.WithCancel(ctx)

	c.Context = context.WithValue(childContext, operationValueKey, c)

	c.event(Event{Type: EventTypeBeginOperation, Operation: c, Fields: fields})

	return c, func() {
		done()
		if d := childContext.Done(); d != nil {
			<-d
		}
		c.end = time.Now()

		c.event(Event{Type: EventTypeCompleteOperation, Operation: c, Fields: fields})
	}
}

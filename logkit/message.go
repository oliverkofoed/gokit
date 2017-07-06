package logkit

import (
	"bytes"
	"fmt"
	"time"
)

type EventType uint8

const (
	EventTypeBeginOperation EventType = iota
	EventTypeCompleteOperation
	EventTypeDebug
	EventTypeInfo
	EventTypeWarn
	EventTypeError
)

type Event struct {
	Message   string
	Type      EventType
	Operation *Context
	Fields    []Field
}

func (m Event) String() string {
	return m.Message //+ "todo(fields)"
}

func (m Event) Error() string {
	var buf bytes.Buffer
	buf.WriteString(m.Message)
	printValues(&buf, m.Fields)
	return buf.String()
}

type FieldType uint8

const (
	FieldTypeUnknown FieldType = iota
	FieldTypeString
	FieldTypeInt64
	FieldTypeBytes
	FieldTypeDuration
	FieldTypeTime
	FieldTypeErr
	FieldTypeStringer
	FieldTypeBool
)

type Field struct {
	Key       string
	FieldType FieldType
	Integer   int64
	Str       string
	Value     interface{}
}

func String(key string, value string) Field {
	return Field{FieldType: FieldTypeString, Key: key, Str: value}
}

func Int64(key string, value int64) Field {
	return Field{FieldType: FieldTypeInt64, Key: key, Integer: value}
}

func Bytes(key string, value []byte) Field {
	return Field{FieldType: FieldTypeBytes, Key: key, Value: value}
}

func Duration(key string, value time.Duration) Field {
	return Field{FieldType: FieldTypeDuration, Key: key, Integer: int64(value)}
}

func Time(key string, value time.Time) Field {
	return Field{FieldType: FieldTypeTime, Key: key, Value: value}
}

func Err(err error) Field {
	return Field{FieldType: FieldTypeErr, Key: "err", Value: err}
}

func Stringer(key string, value fmt.Stringer) Field {
	return Field{FieldType: FieldTypeStringer, Key: key, Value: value}
}

func Bool(key string, value bool) Field {
	if value {
		return Field{FieldType: FieldTypeBool, Key: key, Integer: 1}
	}
	return Field{FieldType: FieldTypeBool, Key: key, Integer: 0}
}

func Int(key string, value int) Field {
	return Field{FieldType: FieldTypeInt64, Key: key, Integer: int64(value)}
}

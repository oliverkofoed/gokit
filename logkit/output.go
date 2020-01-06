package logkit

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"encoding/hex"
)

type Output interface {
	Event(msg Event)
}

func PrintValues(w io.Writer, fields []Field) {
	var anyValues = false
	for i, field := range fields {
		if i == 0 {
			io.WriteString(w, " (")
			anyValues = true
		} else {
			io.WriteString(w, ", ")
		}

		io.WriteString(w, field.Key)
		io.WriteString(w, ": ")
		PrintValue(w, field)
	}
	if anyValues {
		io.WriteString(w, ")")
	}
}

func PrintValue(w io.Writer, field Field) {
	switch field.FieldType {
	case FieldTypeString:
		io.WriteString(w, field.Str)
		break
	case FieldTypeInt64:
		io.WriteString(w, strconv.FormatInt(field.Integer, 10))
		break
	case FieldTypeBytes:
		b := field.Value.([]byte)

		if hex.EncodedLen(len(b)) > maxStringPrintLength {
			io.WriteString(w, hex.EncodeToString(b[:maxStringPrintLength/2]))
			io.WriteString(w, "...")
		} else {
			io.WriteString(w, hex.EncodeToString(b))
		}
		break
	case FieldTypeStringer:
		io.WriteString(w, field.Value.(fmt.Stringer).String())
	case FieldTypeDuration:
		fmt.Fprintf(w, "%v", time.Duration(field.Integer))
	case FieldTypeTime:
		fmt.Fprintf(w, "%v", field.Value)
	case FieldTypeErr:
		fmt.Fprintf(w, "%v", field.Value)
		break
	case FieldTypeBool:
		if field.Integer == 1 {
			io.WriteString(w, "true")
		} else {
			io.WriteString(w, "false")
		}
	case FieldTypeInterface:
		fmt.Fprintf(w, "%v", field.Value)
	default:
		panic(fmt.Sprintf("unknown field type: %v", field.FieldType))
	}
}

func writeShortString(w io.Writer, input string) {
	if len(input) > maxStringPrintLength {
		io.WriteString(w, input[0:maxStringPrintLength-3])
		io.WriteString(w, "...")
	} else {
		io.WriteString(w, input)
	}
}

func colorOutput(w io.Writer, t EventType) {
	switch t {
	case EventTypeDebug:
		w.Write(termGray)
	case EventTypeInfo:
		//w.Write(termGray)
	case EventTypeWarn:
		w.Write(termYellow)
	case EventTypeError:
		w.Write(termRed)
	}
}

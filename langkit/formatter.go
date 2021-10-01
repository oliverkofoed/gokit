package langkit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type formatters struct {
	sync.RWMutex
	m map[string]*formatter
}

func newFormatters() *formatters {
	return &formatters{m: make(map[string]*formatter)}
}

func (f *formatters) get(input string) *formatter {
	f.RLock()
	v, found := f.m[input]
	f.RUnlock()
	if !found {
		v = newFormatter(input)
		f.Lock()
		f.m[input] = v
		f.Unlock()
	}
	return v
}

type formatter struct {
	original string
	parts    []string
	actions  []int
	formats  []string
}

var re = regexp.MustCompile("\\{([a-z0-9])+(\\:([a-z0-9])+)?\\}")

const invalidFormat = 999999

func newFormatter(input string) *formatter {
	v := &formatter{original: input}
	parts := re.FindAllStringSubmatchIndex(input, -1)
	cur := 0
	for _, indexes := range parts {
		part := input[cur:indexes[0]]
		if part != "" {
			v.parts = append(v.parts, part)
			v.actions = append(v.actions, -1)
			v.formats = append(v.formats, "")
		}
		cur = indexes[1]

		directive := input[indexes[0]+1 : indexes[1]-1]
		format := ""
		arg := invalidFormat
		arr := strings.Split(directive, ":")
		if len(arr) >= 1 {
			if arr[0] == "plural" {
				arg = 0
			} else {
				if parsed, err := strconv.ParseInt(arr[0], 10, 32); err == nil {
					arg = int(parsed)
				}
			}
		}
		if len(arr) >= 2 {
			format = arr[1]
		}
		v.parts = append(v.parts, "{"+directive+"}")
		v.actions = append(v.actions, arg)
		v.formats = append(v.formats, format)
	}
	part := input[cur:]
	if part != "" {
		v.parts = append(v.parts, part)
		v.actions = append(v.actions, -1)
		v.formats = append(v.formats, "")
	}

	return v
}

func (f *formatter) formatPlural(count int, args ...interface{}) string {
	var sb strings.Builder
	for i, a := range f.actions {
		if a == -1 {
			sb.WriteString(f.parts[i])
		} else if a == 0 {
			format(&sb, count, f.formats[i])
		} else if a == invalidFormat {
			sb.WriteString("[INVALID: " + f.parts[i] + "]")
		} else {
			ix := a - 1
			if ix >= len(args) {
				sb.WriteString(fmt.Sprintf("[INVALID: missing format arg {%v}]", a))
			} else {
				format(&sb, args[a-1], f.formats[i])
			}
		}
	}
	return sb.String()
}

func (f *formatter) format(args ...interface{}) string {
	return f.formatPlural(0, args...)
}

func format(sb *strings.Builder, value interface{}, format string) {
	if format == "" {
		switch f := value.(type) {
		case int:
			sb.WriteString(strconv.FormatInt(int64(f), 10))
			return
		case int64:
			sb.WriteString(strconv.FormatInt(f, 10))
			return
		case float64:
			sb.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
			return
		case string:
			sb.WriteString(f)
			return
		}
	}

	sb.WriteString(fmt.Sprintf("%v", value))
}

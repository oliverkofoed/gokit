package testkit

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func Assert(t *testing.T, condition bool) {
	if !condition {
		fail(t, "The assertation was not true.")
	}
}

func NoError(t *testing.T, err error) {
	if err != nil {
		fail(t, err.Error())
	}
}

func Equal(t *testing.T, value interface{}, expected interface{}) {
	if !reflect.DeepEqual(value, expected) {
		fail(t, "Values not equal\n\t------- VALUE: "+fmt.Sprintf("%v", value)+"\n\t---- EXPECTED: "+fmt.Sprintf("%v", expected))
	}
}

func Error(t *testing.T, err error) {
	if err == nil {
		fail(t, "Expected an error, but didn't get any.")
	}
}

func Fail(t *testing.T, message string) {
	fail(t, message)
}

func fail(t *testing.T, message string) {
	stack := strings.Split(string(debug.Stack()), "\n")
	var buffer bytes.Buffer

	buffer.WriteString(" == ERROR: " + message + "\n")
	for i, text := range stack {
		if i > 6 {
			buffer.WriteString(text + "\n")
		}
	}

	fmt.Fprintln(os.Stderr, string(buffer.Bytes()))
	time.Sleep(10 * time.Millisecond)
	t.FailNow()
}

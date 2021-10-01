package langkit

import (
	"fmt"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestFormatters(t *testing.T) {
	formatters := newFormatters()

	testkit.Equal(t, formatters.get("hello world").format(), "hello world")
	testkit.Equal(t, formatters.get("hello {1}").format("world"), "hello world")
	testkit.Equal(t, formatters.get("hello {2} {1}").format("world", "cruel"), "hello cruel world")
	testkit.Equal(t, formatters.get("hello {2} {1}. There are {3:d} of you.").format("world", "cruel", 100), "hello cruel world. There are 100 of you.")

	testkit.Equal(t, formatters.get("hello {1}").format(), "hello [INVALID: missing format arg {1}]")
}

func BenchmarkFormatter(b *testing.B) {
	// run the Fib function b.N times
	formatters := newFormatters()

	expected := "hello world 1234 0.234"
	for n := 0; n < b.N; n++ {
		output := formatters.get("hello {1} {2} {3}").format("world", 1234, 0.234)
		if output != expected {
			fmt.Println(output, "!=", expected)
			b.Fail()
		}
	}
}

func BenchmarkFmt(b *testing.B) {
	// run the Fib function b.N times
	expected := "hello world 1234 0.234"
	for n := 0; n < b.N; n++ {
		output := fmt.Sprintf("hello %v %d %v", "world", 1234, 0.234)
		if output != expected {
			fmt.Println(output, "!=", expected)
			b.Fail()
		}
	}
}

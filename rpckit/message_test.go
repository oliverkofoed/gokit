package rpckit

import (
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestMessage(t *testing.T) {
	x := NewMessage(1024)
	x.WriteInt(1)
	x.WriteInt(512)
	x.WriteString("Hello World")
	queue := &testMessageQueue{}
	for i := 0; i != 200; i++ {
		m := queue.makeMessage()
		testkit.NoError(t, queue.checkMessage(m))
	}
}

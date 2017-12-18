package rpckit

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestRPC(t *testing.T) {
	// start a server
	server := NewTestServer()
	go server.ListenAndServe("tcp", "127.0.0.1:8389")
	time.Sleep(100 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	_, err := NewConnection("tcp", "127.0.0.1:8389",
		func(c *Connection, msg *Message) {
			c.Send(msg)
		},
		func(c *Connection, err error) {
			wg.Done()
		},
	)
	testkit.NoError(t, err)
	wg.Wait()

	fmt.Println("done")

	/*return
	// start a server
	server := NewServer(
		func(c *Connection) {
			//wg.Done()
		}, func(c *Connection, msg *Message) {
			c.Send(msg)
		}, func(c *Connection, err error) {
			fmt.Println("Disconnected", err)
		},
	)
	go server.ListenAndServe("tcp", "127.0.0.1:8389")
	time.Sleep(time.Millisecond * 200)

	// TEST1: direct bounce
	queue := &testMessageQueue{}
	connection, err := NewConnection("tcp", "127.0.0.1:8389",
		func(c *Connection, msg *Message) {
			queue.CheckMessage(t, msg)
		},
		func(c *Connection, err error) {
			//wg.Done()
		},
	)
	testkit.NoError(t, err)

	for i := 0; i != 200; i++ {
		m := queue.MakeMessage()
		connection.Send(m)
	}
	queue.Wait()
	connection.Close()

	// TEST2: chop message in various ways
	queue = &testMessageQueue{}
	connection, err = NewConnection("tcp", "127.0.0.1:8389",
		func(c *Connection, msg *Message) {
			queue.CheckMessage(t, msg)
		},
		func(c *Connection, err error) {},
	)
	testkit.NoError(t, err)

	testkit.NoError(t, connection.conn.(*net.TCPConn).SetNoDelay(true))
	for i := 0; i != 100; i++ {
		m := queue.MakeMessage()
		bytes := m.Bytes()

		for len(bytes) > 0 {
			slice := bytes[:rand.Intn(len(bytes)+1)]
			n, err := connection.conn.Write(slice)
			testkit.NoError(t, err)
			bytes = bytes[n:]
			time.Sleep(5 + time.Duration(rand.Intn(50))*time.Millisecond)
		}
	}
	queue.Wait()
	*/
}

package rpckit

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

func NewTestServer() *Server {
	return NewServer(func(c *Connection) {
		fmt.Println("TestServer: New Connection")
		queue := &testMessageQueue{}
		c.Data = queue

		// send 200 messages fast and easy
		for i := 0; i != 200; i++ {
			m := queue.makeMessage()
			c.Send(m)
		}

		// send 100 messages chopped in a varity of ways
		if tcpConn, ok := c.conn.(*net.TCPConn); ok {
			err := tcpConn.SetNoDelay(true)
			if err != nil {
				panic(err)
			}
		}
		for i := 0; i != 100; i++ {
			m := queue.makeMessage()
			bytes := m.Bytes()

			for len(bytes) > 0 {
				slice := bytes[:rand.Intn(len(bytes)+1)]
				n, err := c.conn.Write(slice)
				if err != nil {
					panic(err)
				}
				bytes = bytes[n:]
				time.Sleep(5 + time.Duration(rand.Intn(50))*time.Millisecond)
			}
		}

		// kill connection when done.
		go func() {
			queue.Wait()
			c.Close()
		}()
	}, func(c *Connection, msg *Message) {
		queue := c.Data.(*testMessageQueue)
		if err := queue.checkMessage(msg); err != nil {
			panic(err)
		}
	}, func(c *Connection, err error) {
		fmt.Println("TestServer: Disconnected (", err, ")")
	})
}

type testMessageQueue struct {
	sync.WaitGroup
	sync.RWMutex
	ptr   int
	queue []*testMessage
}

func (q *testMessageQueue) makeMessage() *Message {
	m := newRandomTestMessage()
	q.Lock()
	q.queue = append(q.queue, m)
	q.Add(1)
	q.Unlock()

	return m.getMessage()
}

func (q *testMessageQueue) checkMessage(msg *Message) error {
	q.Lock()
	expected := q.queue[q.ptr]
	q.ptr++
	q.Unlock()
	err := expected.isEqual(msg)
	q.Done()
	return err
}

type testMessage struct {
	Values []interface{}
}

func newRandomTestMessage() *testMessage {
	m := &testMessage{
		Values: make([]interface{}, 0),
	}

	count := rand.Intn(10)
	for i := 0; i <= count; i++ {
		switch rand.Intn(2) {
		case 0:
			m.Values = append(m.Values, uint64(rand.Int63()))
		case 1:
			m.Values = append(m.Values, randomString(rand.Intn(512)))
		}
	}

	return m
}

func (t testMessage) getMessage() *Message {
	m := NewMessage(0)
	for _, v := range t.Values {
		switch val := v.(type) {
		case uint64:
			m.WriteInt(val)
		case string:
			m.WriteString(val)
		}
	}
	return m
}

func (x *testMessage) isEqual(msg *Message) error {
	for _, v := range x.Values {
		switch val := v.(type) {
		case uint64:
			n, err := msg.ReadInt()
			if err != nil {
				return err
			}
			if val != n {
				return fmt.Errorf("Wanted %v to match %v", val, n)
			}
		case string:
			n, err := msg.ReadString()
			if err != nil {
				return err
			}
			if val != n {
				return fmt.Errorf("Wanted %v to match %v", val, n)
			}
		}
	}
	return nil
}

var randomIDRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ01234567890")

func randomString(length int) string {
	b := make([]rune, length)
	for i := range b {
		b[i] = randomIDRunes[rand.Intn(len(randomIDRunes))]
	}
	return string(b)
}

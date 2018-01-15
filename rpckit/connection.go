package rpckit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

type ConnectionHandler func(c *Connection)

type MessageHandler func(c *Connection, msg *Message)

type DisconnectedHandler func(c *Connection, err error)

type Connection struct {
	sync.Mutex
	connected    bool
	conn         net.Conn
	onMessage    MessageHandler
	onDisconnect DisconnectedHandler
	Data         interface{}
}

func NewConnection(network, address string, onMessage MessageHandler, onDisconnect DisconnectedHandler) (*Connection, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return connected(conn, onMessage, onDisconnect), nil
}

func connected(conn net.Conn, onMessage MessageHandler, onDisconnect DisconnectedHandler) *Connection {
	c := &Connection{
		connected:    true,
		conn:         conn,
		onMessage:    onMessage,
		onDisconnect: onDisconnect,
	}
	go c.readLoop()
	return c
}

func (c *Connection) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}
func (c *Connection) readLoop() {
	buf := make([]byte, 0, 512)
	off := 0
	minRead := 256

	// kill bad connections
	defer func() {
		if bad := recover(); bad != nil {
			if err, ok := bad.(error); ok {
				c.end(err)
			} else {
				c.end(fmt.Errorf("unhandled: %v", bad))
			}
		}
	}()

	for {
		// read from the connection
		if off >= len(buf) {
			buf = buf[:0]
			off = 0
		}
		if free := cap(buf) - len(buf); free < minRead {
			//fmt.Println("grow")
			// not enough space at end
			newBuf := buf
			if off+free < minRead {
				// not enough space using beginning of buffer;
				// double buffer capacity
				newBuf = makeSlice(2*cap(buf) + minRead)
			}
			copy(newBuf, buf[off:])
			buf = newBuf[:len(buf)-off]
			off = 0
		}
		m, err := c.conn.Read(buf[len(buf):cap(buf)])
		buf = buf[0 : len(buf)+m]
		if err == io.EOF {
			c.end(nil)
			return
		}
		if err != nil {
			c.end(err)
			return
		}

		for {
			msg, length := MessageFromBytes(buf[off:])
			if msg != nil {
				off += length
				c.onMessage(c, msg)
			} else {
				break
			}
		}
	}
}

var ErrMsgNotFullySent = errors.New("all the message bytes were not sent - perhaps the message is too large")

func (c *Connection) Write(buf []byte) (int, error) {
	return c.conn.Write(buf)
}

func (c *Connection) Send(msg *Message) error {
	bytes := msg.Bytes()
	n, err := c.conn.Write(bytes)
	if err != nil {
		return err
	}
	if n != len(bytes) {
		return ErrMsgNotFullySent
	}
	return nil
}

func (c *Connection) Close() {
	c.end(nil)
}

func (c *Connection) end(err error) {
	c.Lock()
	defer c.Unlock()
	if c.connected {
		c.connected = false

		c.conn.Close()

		d := c.onDisconnect
		if d != nil {
			c.onDisconnect = nil
			d(c, err)
		}
	}
}

func makeSlice(n int) []byte {
	// If the make fails, give a known error.
	defer func() {
		if recover() != nil {
			panic(bytes.ErrTooLarge)
		}
	}()
	return make([]byte, n)
}

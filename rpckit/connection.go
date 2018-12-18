package rpckit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
)

type ConnectionHandler func(c *Connection)

type MessageHandler func(c *Connection, msg *Message)

type DisconnectedHandler func(c *Connection, err error)

// connId is a runtime connection counter
var connId uint64

type Connection struct {
	id           uint64
	closed       int32
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
		id:           atomic.AddUint64(&connId, 1),
		closed:       0,
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

// Close closes the connection
func (c *Connection) Close() error {
	c.end(nil)
	return nil
}

// end will only be called once
func (c *Connection) end(err error) {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		c.conn.Close()

		d := c.onDisconnect

		if d != nil {
			c.onDisconnect = nil
			d(c, err)
		}
	}
}

func (c *Connection) String() string {
	return fmt.Sprintf("Connection(%d)", atomic.LoadUint64(&c.id))
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

package rpckit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

type Message struct {
	buf       []byte
	len       int
	pos       int // for reading
	LastError error
}

func NewMessage(capacity int) *Message {
	m := &Message{
		buf: make([]byte, 4+capacity),
		len: 4, // make room for length prefix
		pos: 4, // make room for length prefix
	}
	return m
}

func MessageFromBytes(msg []byte) (*Message, int) {
	if len(msg) > 4 {
		length := int(binary.BigEndian.Uint32(msg))
		if len(msg) >= length {
			m := &Message{
				buf: msg,
				len: len(msg),
				pos: 4,
			}
			return m, length
		}
	}
	return nil, 0
}

func (m *Message) Bytes() []byte {
	b := m.buf[:m.len]
	binary.BigEndian.PutUint32(b, uint32(m.len))
	return b
}

func (m *Message) WriteInt(v uint64) {
	m.grow(10)
	//i := 0
	for v >= 0x80 {
		m.buf[m.len] = byte(v) | 0x80
		m.len++
		v >>= 7
		//i++
	}
	m.buf[m.len] = byte(v)
	m.len++
}

func (m *Message) WriteString(v string) {
	stringLength := len(v)
	m.WriteInt(uint64(stringLength))

	m.grow(stringLength)
	copy(m.buf[m.len:], v)
	m.len += stringLength
}

func (m *Message) WriteBytes(v []byte) {
	byteLength := len(v)
	m.WriteInt(uint64(byteLength))

	m.grow(byteLength)
	copy(m.buf[m.len:], v)
	m.len += byteLength
}

func (m *Message) grow(needed int) {
	if m.len+needed > len(m.buf) {
		// Not enough space anywhere, we need to allocate.
		buf := makeSlice(2*cap(m.buf) + needed)
		copy(buf, m.buf[0:m.len])
		m.buf = buf
	}
}

func (m *Message) ReadString() (string, error) {
	v, err := m.ReadInt()
	if err != nil {
		m.LastError = err
		return "", err
	}
	length := int(v)

	if m.pos+length > m.len {
		m.LastError = io.EOF
		return "", io.EOF
	}

	str := m.buf[m.pos : m.pos+length]
	m.pos += length

	return string(str), nil
}

var ErrOverflow = errors.New("Overflow in varint")

func (m *Message) ReadInt() (uint64, error) {
	var x uint64
	var s uint
	for i := 0; ; i++ {
		if m.pos+1 > m.len {
			m.LastError = io.EOF
			return 0, io.EOF
		}
		b := m.buf[m.pos]
		m.pos++
		if b < 0x80 {
			if i > 9 || (i == 9 && b > 1) {
				m.LastError = ErrOverflow
				return 0, ErrOverflow
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
}


func (m *Message) ReadFloat64() (float64, error) {
	if m.pos+8 > m.len {
		m.LastError = io.EOF
		return 0, io.EOF
	}

	b := m.buf[m.pos : m.pos+8]
	m.pos += 8
	var value float64
	buf := bytes.NewReader(b)
	err := binary.Read(buf, binary.LittleEndian, &value)
	if err != nil {
		return 0, err
	}
	return value, err
}

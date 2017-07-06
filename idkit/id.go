package idkit

import (
	"sync"
	"sync/atomic"
	"time"
)

type IDSpace struct {
	sync.Mutex
	processID byte
	sequence  uint32
	offset    int64
	lastTime  int64
}

func NewIDSpace(processID byte, minTime time.Time) *IDSpace {
	return &IDSpace{
		processID: processID,
		sequence:  0,
		offset:    minTime.Unix(),
	}
}

func (i *IDSpace) MakeID(t time.Time) []byte {
	if t.Location() != time.UTC {
		panic("Only call MakeID() with UTC times")
	}

	tunix := t.Unix()

	i.Lock()
	if tunix > i.lastTime {
		i.lastTime = tunix
		i.sequence = 0
	}
	s := atomic.AddUint32(&i.sequence, 1)
	i.Unlock()

	b := make([]byte, 8, 8)
	tx := tunix - i.offset
	b[0], b[1], b[2], b[3] = byte(tx>>24), byte(tx>>16), byte(tx>>8), byte(tx)
	b[4], b[5], b[6], b[7] = i.processID, byte(s>>16), byte(s>>8), byte(s)
	return b
}

func (i *IDSpace) ParseTime(id []byte) time.Time {
	seconds := int64(uint32(id[0])<<24 | uint32(id[1])<<16 | uint32(id[2])<<8 | uint32(id[3]))
	return time.Unix(i.offset+seconds, 0).UTC()
}

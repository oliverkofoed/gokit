package idkit

import (
	"testing"
	"time"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestID(t *testing.T) {

	start := time.Date(2015, 0, 0, 0, 0, 0, 0, time.UTC)

	idspace0 := NewIDSpace(0, start)
	idspace1 := NewIDSpace(1, start)

	expect := func(i *IDSpace, ti time.Time, b []byte) {
		id := i.MakeID(ti)
		testkit.Equal(t, id, b)
		testkit.Equal(t, i.ParseTime(id), ti)
	}

	expect(idspace0, start, []byte{0, 0, 0, 0, 0, 0, 0, 1})
	expect(idspace0, start, []byte{0, 0, 0, 0, 0, 0, 0, 2})
	expect(idspace1, start, []byte{0, 0, 0, 0, 1, 0, 0, 1})
	expect(idspace1, start, []byte{0, 0, 0, 0, 1, 0, 0, 2})

	expect(idspace0, start.Add(time.Second), []byte{0, 0, 0, 1, 0, 0, 0, 1})
	expect(idspace0, start.Add(time.Second), []byte{0, 0, 0, 1, 0, 0, 0, 2})
	expect(idspace0, start.Add(time.Second), []byte{0, 0, 0, 1, 0, 0, 0, 3})
	expect(idspace0, start.Add(time.Second*2), []byte{0, 0, 0, 2, 0, 0, 0, 1})
}

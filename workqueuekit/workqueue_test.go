package workqueuekit

import (
	"sync/atomic"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestWorkQueue(t *testing.T) {
	ctr := int64(0)
	work := New(10, 100)
	for i := 0; i != 100; i++ {
		work.QueueWork(func() {
			atomic.AddInt64(&ctr, 1)
		})
	}
	work.Done()
	testkit.Equal(t, ctr, int64(100))
}

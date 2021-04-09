package workqueuekit

import (
	"sync"
)

type WorkQueue struct {
	queue chan func()
	wg    sync.WaitGroup
}

func New(workerCount int, maxQueueSize int) *WorkQueue {
	w := &WorkQueue{
		queue: make(chan func(), maxQueueSize),
	}

	for i := 0; i < workerCount; i++ {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			for work := range w.queue {
				work()
			}
		}()
	}

	return w
}

func (w *WorkQueue) QueueWork(work func()) {
	w.queue <- work
}

func (w *WorkQueue) Done() {
	close(w.queue)
	w.wg.Wait()
}

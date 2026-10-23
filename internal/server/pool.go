package server

import (
	"context"
	"sync"
)

// pool runs queued work on a fixed number of workers, under the pool's
// lifetime context: cancelling it cancels the work in flight. Each item is
// a pull request key (see reviews), which the worker drains.
type pool struct {
	ctx  context.Context
	jobs chan string
	run  func(context.Context, string)
	wg   sync.WaitGroup
}

// newPool starts workers running run on submitted keys, until ctx is done.
// Up to queue keys wait for a worker.
func newPool(ctx context.Context, workers, queue int, run func(context.Context, string)) *pool {
	p := &pool{ctx: ctx, jobs: make(chan string, queue), run: run}
	p.wg.Add(workers)
	for range workers {
		go p.work()
	}
	return p
}

// submit queues key without blocking; false when the queue is full or the
// pool is stopping.
func (p *pool) submit(key string) bool {
	if p.ctx.Err() != nil {
		return false
	}
	select {
	case p.jobs <- key:
		return true
	default:
		return false
	}
}

// wait blocks until every worker has stopped. Keys still queued when the
// lifetime context ends are dropped.
func (p *pool) wait() { p.wg.Wait() }

// work runs keys until the lifetime context is done.
func (p *pool) work() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case key := <-p.jobs:
			p.run(p.ctx, key)
		}
	}
}

package server

import (
	"context"
	"sync"
	"time"
)

// pool runs queued reviews on a fixed number of workers. Each review runs
// under a context derived from the pool's lifetime context with the review
// timeout, so cancelling the lifetime context cancels every review in
// flight and no review runs unbounded.
type pool struct {
	ctx     context.Context
	jobs    chan job
	timeout time.Duration
	run     func(context.Context, job)
	wg      sync.WaitGroup
}

// newPool starts workers running run on submitted jobs, until ctx is done.
// Up to queue jobs wait for a worker.
func newPool(ctx context.Context, workers, queue int, timeout time.Duration, run func(context.Context, job)) *pool {
	p := &pool{ctx: ctx, jobs: make(chan job, queue), timeout: timeout, run: run}
	p.wg.Add(workers)
	for range workers {
		go p.work()
	}
	return p
}

// submit queues j without blocking; false when the queue is full or the
// pool is stopping.
func (p *pool) submit(j job) bool {
	if p.ctx.Err() != nil {
		return false
	}
	select {
	case p.jobs <- j:
		return true
	default:
		return false
	}
}

// wait blocks until every worker has stopped. Jobs still queued when the
// lifetime context ends are dropped.
func (p *pool) wait() { p.wg.Wait() }

// work runs jobs until the lifetime context is done.
func (p *pool) work() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case j := <-p.jobs:
			ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
			p.run(ctx, j)
			cancel()
		}
	}
}

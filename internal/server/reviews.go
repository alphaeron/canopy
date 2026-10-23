package server

import (
	"context"
	"sync"
	"time"
)

// reviews schedules reviews so that only the newest review of a pull
// request's key (PullRequest.Key) reports. A key's reviews run one at a
// time, on one worker, so no two of them report at once: a review that
// starts later reports later. A review requested while another of its key
// waits replaces it, and one requested while another runs cancels it and
// runs next. A review checks it is still the newest just before it
// reports, and a superseded review does not report at all.
//
// A key waits in the pool once, however many reviews it is asked for, so a
// burst of webhooks for one pull request takes one place in the queue.
type reviews struct {
	pool    *pool
	timeout time.Duration
	review  func(ctx context.Context, j job, current func() bool)

	mu    sync.Mutex
	slots map[string]*slot // keys queued or running
}

// slot is one key's scheduling state.
type slot struct {
	gen    uint64             // the newest request's generation
	next   *job               // the newest request not yet started
	cancel context.CancelFunc // cancels the running review; nil while none runs
}

// job is one requested review.
type job struct {
	platform string
	pr       PullRequest
	gen      uint64 // its generation within its key
}

// newReviews schedules reviews on workers, each bounded by timeout, until
// ctx is done. Up to queue keys wait for a worker. review runs one review;
// current reports whether it is still its key's newest.
func newReviews(ctx context.Context, workers, queue int, timeout time.Duration, review func(context.Context, job, func() bool)) *reviews {
	r := &reviews{timeout: timeout, review: review, slots: map[string]*slot{}}
	r.pool = newPool(ctx, workers, queue, r.drain)
	return r
}

// submit requests a review of j's pull request, replacing or cancelling
// any earlier review of its key; false when the queue is full or the
// server is stopping.
func (r *reviews) submit(j job) bool {
	if r.pool.ctx.Err() != nil {
		return false
	}
	key := j.pr.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.slots[key]; ok {
		s.gen++
		j.gen = s.gen
		s.next = &j
		if s.cancel != nil {
			s.cancel()
		}
		return true
	}
	j.gen = 1
	if !r.pool.submit(key) {
		return false
	}
	r.slots[key] = &slot{gen: j.gen, next: &j}
	return true
}

// drain runs key's reviews, newest first, until none is waiting. A key
// whose worker is stopping is dropped with its waiting review.
func (r *reviews) drain(ctx context.Context, key string) {
	for {
		r.mu.Lock()
		s := r.slots[key]
		if s.next == nil || ctx.Err() != nil {
			delete(r.slots, key)
			r.mu.Unlock()
			return
		}
		j := *s.next
		s.next = nil
		rctx, cancel := context.WithTimeout(ctx, r.timeout)
		s.cancel = cancel
		r.mu.Unlock()

		r.review(rctx, j, func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return s.gen == j.gen
		})

		r.mu.Lock()
		s.cancel = nil
		r.mu.Unlock()
		cancel()
	}
}

// wait blocks until every worker has stopped.
func (r *reviews) wait() { r.pool.wait() }

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
//
// A request still waiting a while after it arrives may publish that it is
// queued (claimMark). A review waits for any queued status being published
// as it starts before it publishes its own, and once it starts no older
// request's queued status is published, so a queued status never lands
// after a newer review's.
type reviews struct {
	pool    *pool
	timeout time.Duration
	review  func(ctx context.Context, j job, current func() bool)
	// dropped, when set, is told of each job replaced while it waited,
	// which never runs. It is called with the scheduler's lock held.
	dropped func(j job)
	// accepted, when set, is told of each job submit accepts, as
	// scheduled (its generation and slot set). It is called with the
	// scheduler's lock held, so it must not block.
	accepted func(j job)

	mu    sync.Mutex
	slots map[string]*slot // keys queued or running
}

// slot is one key's scheduling state.
type slot struct {
	gen     uint64             // the newest request's generation
	next    *job               // the newest request not yet started
	cancel  context.CancelFunc // cancels the running review; nil while none runs
	started uint64             // the newest started request's generation
	marking chan struct{}      // closed when the queued status publishing ends; nil while none is
}

// job is one requested review.
type job struct {
	platform   string
	pr         PullRequest
	gen        uint64 // its generation within its key, on this server
	deliveryID string // the delivery that requested it
	queuedAt   time.Time
	order      int64       // its order among every replica's requests for its key (Recorder); 0 when unknown
	live       *liveReview // its progress, for its review page
	slot       *slot       // its key's scheduling state, set by submit
	// marked is closed once the queued status being published when its
	// review started is; nil when none was.
	marked <-chan struct{}
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
		j.slot = s
		if s.next != nil && r.dropped != nil {
			r.dropped(*s.next)
		}
		s.next = &j
		if s.cancel != nil {
			s.cancel()
		}
		r.accept(j)
		return true
	}
	j.gen = 1
	if !r.pool.submit(key) {
		return false
	}
	s := &slot{gen: j.gen}
	j.slot = s
	s.next = &j
	r.slots[key] = s
	r.accept(j)
	return true
}

// accept tells accepted of j, if it is set.
func (r *reviews) accept(j job) {
	if r.accepted != nil {
		r.accepted(j)
	}
}

// claimMark reports whether j's queued status may be published now: no
// review of its key requested as late as j or later has started, and no
// other queued status of its key is being published. When it may, release
// must be called once publishing ends, which lets the review starting next
// publish its own statuses.
func (r *reviews) claimMark(j job) (release func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := j.slot
	if r.pool.ctx.Err() != nil || s == nil || r.slots[j.pr.Key()] != s || s.started >= j.gen || s.marking != nil {
		return nil, false
	}
	done := make(chan struct{})
	s.marking = done
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		close(done)
		s.marking = nil
	}, true
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
		s.started = j.gen
		j.marked = s.marking
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

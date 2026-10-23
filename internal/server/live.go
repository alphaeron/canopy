package server

import (
	"slices"
	"sync"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

// pageRef identifies a review page: a pull request's head commit.
type pageRef struct {
	repo    string // "owner/name", lower-cased
	number  int
	headSHA string
}

func pageRefOf(pr PullRequestInfo) pageRef { return pageRef{pr.Repo, pr.Number, pr.HeadSHA} }

// The stages of a live review, besides the approval.Progress phases it
// passes through while it evaluates.
const (
	stageQueued   = "queued"
	stageReport   = "report"
	stageFinished = "finished"
)

// liveReview is a review this server queued or ran, as its review page
// shows it while it runs.
type liveReview struct {
	// Stage is stageQueued, an approval.Progress phase, stageReport, or
	// stageFinished.
	Stage string
	// Done and Total count the files approved so far (see
	// approval.Progress).
	Done, Total int
	QueuedAt    time.Time
	StartedAt   time.Time // zero while queued
	FinishedAt  time.Time // zero until finished
	// Status is how the review ended (one of the Review* constants), and
	// Outcome what it reached or published, once finished.
	Status  string
	Outcome approval.Outcome
	// State is the review's state once finished, kept only when the
	// server has no ReviewHistory to read it back from.
	State *approval.ReviewState
}

// maxFinishedLive is how many finished reviews the tracker keeps, so a
// review page opened just after its review ends still finds it.
const maxFinishedLive = 32

// tracker follows the reviews this server queues and runs, for their
// review pages. Each queued review gets its own entry, and a page shows its
// newest: a superseded review's later progress updates its own entry, never
// its successor's. Entries of queued and running reviews are bounded by the
// review queue; finished ones by maxFinishedLive. It is safe for concurrent
// use.
type tracker struct {
	keepState bool // keep finished reviews' states (no ReviewHistory)

	mu       sync.Mutex
	pages    map[pageRef]*liveReview
	finished []pageRef // pages whose newest review finished, oldest first
}

func newTracker(keepState bool) *tracker {
	return &tracker{keepState: keepState, pages: map[pageRef]*liveReview{}}
}

// queue tracks a review of ref queued at at, as ref's newest, and returns
// its entry.
func (t *tracker) queue(ref pageRef, at time.Time) *liveReview {
	l := &liveReview{Stage: stageQueued, QueuedAt: at}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pages[ref] = l
	return l
}

// forget stops tracking l, a review of ref that will never run.
func (t *tracker) forget(ref pageRef, l *liveReview) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pages[ref] == l {
		delete(t.pages, ref)
	}
}

// start marks l running from at.
func (t *tracker) start(l *liveReview, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.StartedAt = at
}

// progress records l reaching p.
func (t *tracker) progress(l *liveReview, p approval.Progress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.Stage, l.Done, l.Total = p.Phase, p.Done, p.Total
}

// reporting marks l publishing its outcome.
func (t *tracker) reporting(l *liveReview) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.Stage = stageReport
}

// finish marks l, a review of ref, finished as end says, and keeps it among
// the finished if it is still ref's newest.
func (t *tracker) finish(ref pageRef, l *liveReview, end ReviewEnd, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.Stage, l.FinishedAt, l.Status, l.Outcome = stageFinished, at, end.Status, end.Outcome
	if t.keepState {
		l.State = end.State
	}
	if t.pages[ref] != l {
		return
	}
	t.finished = append(slices.DeleteFunc(t.finished, func(r pageRef) bool { return r == ref }), ref)
	for len(t.finished) > maxFinishedLive {
		old := t.finished[0]
		t.finished = t.finished[1:]
		// A page queued again since is not finished: keep its review.
		if p := t.pages[old]; p != nil && p.Stage == stageFinished {
			delete(t.pages, old)
		}
	}
}

// get is a copy of ref's newest review, if one is tracked.
func (t *tracker) get(ref pageRef) (liveReview, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.pages[ref]
	if !ok {
		return liveReview{}, false
	}
	return *l, true
}

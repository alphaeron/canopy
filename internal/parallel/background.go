package parallel

import (
	"context"
	"sync"
	"time"
)

// Background runs calls nobody waits for, such as writing what a review
// read to a cache, so they never delay the caller. At most a limit run at
// once; a call started while that many run is dropped, so a slow backend
// sheds work instead of piling it up. Each call runs under its own timeout,
// detached from the caller's context, which may end first. It is safe for
// concurrent use; a nil Background drops every call.
type Background struct {
	sem     chan struct{}
	timeout time.Duration
	wg      sync.WaitGroup
}

// NewBackground runs at most limit calls at once (at least 1), each for at
// most timeout.
func NewBackground(limit int, timeout time.Duration) *Background {
	return &Background{sem: make(chan struct{}, max(limit, 1)), timeout: timeout}
}

// Go starts f, unless limit calls are already running; false when it was
// dropped.
func (b *Background) Go(f func(ctx context.Context)) bool {
	if b == nil {
		return false
	}
	select {
	case b.sem <- struct{}{}:
	default:
		return false
	}
	b.wg.Go(func() {
		defer func() { <-b.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		defer cancel()
		f(ctx)
	})
	return true
}

// Wait blocks until every call started so far has returned.
func (b *Background) Wait() {
	if b != nil {
		b.wg.Wait()
	}
}

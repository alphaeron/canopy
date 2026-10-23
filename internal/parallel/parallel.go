// Package parallel runs independent calls concurrently, a bounded number at
// a time. It imports only the standard library, so any package may depend
// on it.
package parallel

import "sync"

// Limit is how many platform calls one review makes at once: enough to
// overlap their round trips, and few enough that the reviews a server runs
// together stay well under GitHub's limit on concurrent requests.
const Limit = 8

// For calls f(i) for every i in [0, n), at most limit at a time, and
// returns once every call has returned. The calls run concurrently, so each
// should write only its own result (e.g. index i of a slice). A limit below
// 1 runs the calls one at a time.
func For(n, limit int, f func(i int)) {
	sem := make(chan struct{}, max(limit, 1))
	var wg sync.WaitGroup
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			f(i)
		})
	}
	wg.Wait()
}

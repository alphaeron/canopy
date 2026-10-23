package server

import "sync"

// bounded is a map holding at most a limit of entries. Adding to a full map
// evicts an arbitrary entry, so it suits values that never go stale, where
// an eviction costs at most a later read. It is safe for concurrent use.
type bounded[K comparable, V any] struct {
	limit int

	mu sync.Mutex
	m  map[K]V
}

// newBounded is an empty bounded holding at most limit entries (at least 1).
func newBounded[K comparable, V any](limit int) *bounded[K, V] {
	limit = max(limit, 1)
	return &bounded[K, V]{limit: limit, m: make(map[K]V, limit)}
}

// get returns the value kept for k.
func (b *bounded[K, V]) get(k K) (V, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[k]
	return v, ok
}

// put keeps v for k, evicting an arbitrary entry when full.
func (b *bounded[K, V]) put(k K, v V) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[k]; !ok && len(b.m) >= b.limit {
		for old := range b.m {
			delete(b.m, old)
			break
		}
	}
	b.m[k] = v
}

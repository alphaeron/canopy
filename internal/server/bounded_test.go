package server

import "testing"

// TestBoundedCacheEvicts: a full bounded makes room for a new key, replaces an
// existing one in place, and never exceeds its limit.
func TestBoundedCacheEvicts(t *testing.T) {
	b := newBounded[string, int](2)
	b.put("a", 1)
	b.put("b", 2)
	b.put("b", 3)
	if v, ok := b.get("a"); !ok || v != 1 {
		t.Errorf("replacing b evicted a: Get(a) = %d, %v", v, ok)
	}
	b.put("c", 4)
	if v, ok := b.get("c"); !ok || v != 4 {
		t.Errorf("Get(c) = %d, %v; want 4, true", v, ok)
	}
	if len(b.m) != 2 {
		t.Errorf("len = %d, want the limit 2", len(b.m))
	}
}

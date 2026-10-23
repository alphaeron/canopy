package server

import (
	"bytes"
	"testing"
)

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

// TestCacheScopeBlobs: blobs are kept as copies, a blob over maxCachedBlobBytes is not
// kept, and another scope sees none of them.
func TestCacheScopeBlobs(t *testing.T) {
	repo := NewRepoCache()
	s := repo.Scope("a")
	data := []byte("rules")
	s.PutBlob("k", data)
	data[0] = 'X'
	got, ok := s.Blob("k")
	if !ok || !bytes.Equal(got, []byte("rules")) {
		t.Fatalf("Blob(k) = %q, %v; want the contents as put", got, ok)
	}
	got[0] = 'Y'
	if again, _ := s.Blob("k"); !bytes.Equal(again, []byte("rules")) {
		t.Errorf("modifying a returned blob changed the kept one: %q", again)
	}
	if _, ok := repo.Scope("b").Blob("k"); ok {
		t.Error("scope b read a blob scope a kept")
	}

	s.PutBlob("big", make([]byte, maxCachedBlobBytes+1))
	if _, ok := s.Blob("big"); ok {
		t.Error("kept a blob over maxCachedBlobBytes")
	}
}

// TestCacheScopeTrees: a listing is kept per repository and commit, and another
// scope sees none of them.
func TestCacheScopeTrees(t *testing.T) {
	repo := NewRepoCache()
	s := repo.Scope("a")
	s.PutTree("o/r", "c1", map[string]TreeBlob{"KIT": {SHA: "b1", Size: 1}})
	if got, ok := s.Tree("o/r", "c1"); !ok || got["KIT"].SHA != "b1" {
		t.Errorf("Tree(o/r, c1) = %v, %v; want the listing as put", got, ok)
	}
	for _, miss := range []struct{ scope, repo, commit string }{
		{"a", "o/r", "c2"}, {"a", "o/other", "c1"}, {"b", "o/r", "c1"},
	} {
		if _, ok := repo.Scope(miss.scope).Tree(miss.repo, miss.commit); ok {
			t.Errorf("scope %s read a listing of %s@%s it never kept", miss.scope, miss.repo, miss.commit)
		}
	}
}

// TestNilRepoCacheKeepsNothing: scopes of a nil RepoCache, and the zero CacheScope, are
// valid caches that keep nothing.
func TestNilRepoCacheKeepsNothing(t *testing.T) {
	var nilRepo *RepoCache
	for name, s := range map[string]CacheScope{"nil RepoCache": nilRepo.Scope("a"), "zero CacheScope": {}} {
		s.PutTree("o/r", "c", map[string]TreeBlob{"a": {SHA: "1"}})
		s.PutBlob("b", []byte("x"))
		if _, ok := s.Tree("o/r", "c"); ok {
			t.Errorf("%s returned a tree", name)
		}
		if _, ok := s.Blob("b"); ok {
			t.Errorf("%s returned a blob", name)
		}
	}
}

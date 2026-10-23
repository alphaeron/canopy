package server

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git's object IDs
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// gitBlobSHA is git's SHA-1 object ID of a blob holding data.
func gitBlobSHA(data string) string {
	sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(data), data)) //nolint:gosec // git's object IDs
	return hex.EncodeToString(sum[:])
}

// fakeStore is a RepoStore in memory that counts its calls and can fail.
type fakeStore struct {
	mu        sync.Mutex
	fail      bool
	trees     map[string]map[string]TreeBlob
	blobs     map[string][]byte
	responses map[string]CachedResponse
	reads     int
}

func newFakeStore() *fakeStore {
	return &fakeStore{trees: map[string]map[string]TreeBlob{}, blobs: map[string][]byte{}, responses: map[string]CachedResponse{}}
}

var errStoreDown = errors.New("store down")

func get[T any](f *fakeStore, m map[string]T, key string) (T, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	var zero T
	if f.fail {
		return zero, false, errStoreDown
	}
	v, ok := m[key]
	return v, ok, nil
}

func put[T any](f *fakeStore, m map[string]T, key string, v T) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errStoreDown
	}
	m[key] = v
	return nil
}

func (f *fakeStore) Tree(_ context.Context, scope, repo, commit string) (map[string]TreeBlob, bool, error) {
	return get(f, f.trees, scope+"|"+repo+"|"+commit)
}

func (f *fakeStore) PutTree(_ context.Context, scope, repo, commit string, l map[string]TreeBlob) error {
	return put(f, f.trees, scope+"|"+repo+"|"+commit, l)
}

func (f *fakeStore) Blob(_ context.Context, scope, sha string) ([]byte, bool, error) {
	return get(f, f.blobs, scope+"|"+sha)
}

func (f *fakeStore) PutBlob(_ context.Context, scope, sha string, data []byte) error {
	return put(f, f.blobs, scope+"|"+sha, data)
}

func (f *fakeStore) Response(_ context.Context, scope, url string) (CachedResponse, bool, error) {
	return get(f, f.responses, scope+"|"+url)
}

func (f *fakeStore) PutResponse(_ context.Context, scope, url string, r CachedResponse) error {
	return put(f, f.responses, scope+"|"+url, r)
}

// TestCacheScopeBlobs: blobs are kept as copies, a blob over maxCachedBlobBytes is not
// kept, and another scope sees none of them.
func TestCacheScopeBlobs(t *testing.T) {
	repo := NewRepoCache(nil, nil)
	s := repo.Scope("a")
	data := []byte("rules")
	s.PutBlob("k", data)
	data[0] = 'X'
	got, ok := s.Blob(t.Context(), "k")
	if !ok || !bytes.Equal(got, []byte("rules")) {
		t.Fatalf("Blob(k) = %q, %v; want the contents as put", got, ok)
	}
	got[0] = 'Y'
	if again, _ := s.Blob(t.Context(), "k"); !bytes.Equal(again, []byte("rules")) {
		t.Errorf("modifying a returned blob changed the kept one: %q", again)
	}
	if _, ok := repo.Scope("b").Blob(t.Context(), "k"); ok {
		t.Error("scope b read a blob scope a kept")
	}

	s.PutBlob("big", make([]byte, maxCachedBlobBytes+1))
	if _, ok := s.Blob(t.Context(), "big"); ok {
		t.Error("kept a blob over maxCachedBlobBytes")
	}
}

// TestCacheScopeTrees: a listing is kept per repository and commit, and another
// scope sees none of them.
func TestCacheScopeTrees(t *testing.T) {
	repo := NewRepoCache(nil, nil)
	s := repo.Scope("a")
	s.PutTree("o/r", "c1", map[string]TreeBlob{"RULES": {SHA: "b1", Size: 1}})
	if got, ok := s.Tree(t.Context(), "o/r", "c1"); !ok || got["RULES"].SHA != "b1" {
		t.Errorf("Tree(o/r, c1) = %v, %v; want the listing as put", got, ok)
	}
	for _, miss := range []struct{ scope, repo, commit string }{
		{"a", "o/r", "c2"}, {"a", "o/other", "c1"}, {"b", "o/r", "c1"},
	} {
		if _, ok := repo.Scope(miss.scope).Tree(t.Context(), miss.repo, miss.commit); ok {
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
		s.PutResponse("u", CachedResponse{ETag: `"e"`})
		if _, ok := s.Tree(t.Context(), "o/r", "c"); ok {
			t.Errorf("%s returned a tree", name)
		}
		if _, ok := s.Blob(t.Context(), "b"); ok {
			t.Errorf("%s returned a blob", name)
		}
		if _, ok := s.Response(t.Context(), "u"); ok {
			t.Errorf("%s returned a response", name)
		}
	}
	nilRepo.Wait()
}

// TestRepoCacheWritesThroughToItsStore: what one cache puts, another over
// the same store (another replica) reads, scope by scope.
func TestRepoCacheWritesThroughToItsStore(t *testing.T) {
	store := newFakeStore()
	writer := NewRepoCache(store, nil)
	sha := gitBlobSHA("rules")
	w := writer.Scope("a")
	w.PutTree("o/r", "c1", map[string]TreeBlob{"RULES": {SHA: sha, Size: 5}})
	w.PutBlob(sha, []byte("rules"))
	w.PutResponse("https://api/users/x", CachedResponse{ETag: `"e1"`, Header: http.Header{"Link": {"<next>"}}, Body: []byte("{}")})
	writer.Wait()

	reader := NewRepoCache(store, nil)
	r := reader.Scope("a")
	if got, ok := r.Tree(t.Context(), "o/r", "c1"); !ok || got["RULES"].SHA != sha {
		t.Errorf("Tree = %v, %v; want the listing the other cache put", got, ok)
	}
	if got, ok := r.Blob(t.Context(), sha); !ok || string(got) != "rules" {
		t.Errorf("Blob = %q, %v; want the contents the other cache put", got, ok)
	}
	if got, ok := r.Response(t.Context(), "https://api/users/x"); !ok || got.ETag != `"e1"` || got.Header.Get("Link") != "<next>" {
		t.Errorf("Response = %+v, %v; want the response the other cache put", got, ok)
	}
	if _, ok := reader.Scope("b").Blob(t.Context(), sha); ok {
		t.Error("scope b read a blob scope a stored")
	}

	// A second read is served from memory.
	reads := store.reads
	r.Tree(t.Context(), "o/r", "c1")
	r.Blob(t.Context(), sha)
	if store.reads != reads {
		t.Errorf("store reads after warming = %d, want %d", store.reads, reads)
	}
}

// TestRepoCacheRejectsStoredBlobsNotMatchingTheirSHA: a stored blob whose
// contents were changed is a miss, so a store written to by someone else
// cannot change a rules file.
func TestRepoCacheRejectsStoredBlobsNotMatchingTheirSHA(t *testing.T) {
	store := newFakeStore()
	sha := gitBlobSHA("rules")
	store.blobs["a|"+sha] = []byte("tampered")
	store.blobs["a|nothex"] = []byte("x")
	cache := NewRepoCache(store, nil)
	for _, key := range []string{sha, "nothex"} {
		if got, ok := cache.Scope("a").Blob(t.Context(), key); ok {
			t.Errorf("Blob(%s) = %q, want a miss", key, got)
		}
	}
}

// TestRepoCacheTreatsStoreFailuresAsMisses: a store that fails costs a
// miss, never an error or a wrong answer, and what is put is still kept in
// memory.
func TestRepoCacheTreatsStoreFailuresAsMisses(t *testing.T) {
	store := newFakeStore()
	store.fail = true
	cache := NewRepoCache(store, nil)
	s := cache.Scope("a")
	if _, ok := s.Tree(t.Context(), "o/r", "c1"); ok {
		t.Error("Tree hit with the store down")
	}
	s.PutTree("o/r", "c1", map[string]TreeBlob{})
	cache.Wait()
	if _, ok := s.Tree(t.Context(), "o/r", "c1"); !ok {
		t.Error("a listing put with the store down was not kept in memory")
	}
}

func TestBlobHasSHA(t *testing.T) {
	// git hash-object of "hello\n", in a SHA-1 and a SHA-256 repository.
	tests := []struct {
		sha  string
		want bool
	}{
		{"ce013625030ba8dba906f756967f9e9ca394464a", true},
		{"CE013625030BA8DBA906F756967F9E9CA394464A", true},
		{"2cf8d83d9ee29543b34a87727421fdecb7e3f3a183d337639025de576db9ebb4", true},
		{"ce013625030ba8dba906f756967f9e9ca394464b", false},
		{"ce01", false},
	}
	for _, tt := range tests {
		if got := blobHasSHA([]byte("hello\n"), tt.sha); got != tt.want {
			t.Errorf("blobHasSHA(hello, %s) = %v, want %v", tt.sha, got, tt.want)
		}
	}
}

package ghplatform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer is a token endpoint whose exchanges for blocked installations
// wait until released or until the exchange's client gives up. It counts
// exchanges per installation and signals each one's arrival.
type tokenServer struct {
	blocked map[string]chan struct{} // installation to its release
	arrived chan string              // each exchange's installation, as it arrives

	mu    sync.Mutex
	calls map[string]int
}

// auth starts the server and returns an appAuth for it.
func (s *tokenServer) auth(t *testing.T) *appAuth {
	t.Helper()
	key, err := testKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s.calls = map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s.mu.Lock()
		s.calls[id]++
		s.mu.Unlock()
		if s.arrived != nil {
			s.arrived <- id
		}
		if release, ok := s.blocked[id]; ok {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeJSON(t, w, map[string]string{"token": "inst-" + id, "expires_at": "2099-01-01T00:00:00Z"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &appAuth{id: testAppID, key: key, baseURL: srv.URL + "/", transport: http.DefaultTransport, now: time.Now}
}

// count is how many exchanges installation id made.
func (s *tokenServer) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[id]
}

// TestTokenSharesOneExchangePerInstallation: concurrent first requests of
// one installation share a single token exchange.
func TestTokenSharesOneExchangePerInstallation(t *testing.T) {
	s := &tokenServer{}
	auth := s.auth(t)
	var wg sync.WaitGroup
	var failed atomic.Int32
	for range 20 {
		wg.Go(func() {
			if tok, err := auth.token(t.Context(), 1); err != nil || tok != "inst-1" {
				failed.Add(1)
			}
		})
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Errorf("%d callers did not get the installation's token", failed.Load())
	}
	if got := s.count("1"); got != 1 {
		t.Errorf("token exchanges = %d, want 1 shared by every caller", got)
	}
}

// TestTokenExchangesDoNotBlockOtherInstallations: an installation whose
// exchange is stuck holds up neither another installation nor a caller of
// its own whose ctx is done.
func TestTokenExchangesDoNotBlockOtherInstallations(t *testing.T) {
	release := make(chan struct{})
	s := &tokenServer{blocked: map[string]chan struct{}{"1": release}, arrived: make(chan string, 4)}
	auth := s.auth(t)

	stuck := make(chan error, 1)
	go func() {
		_, err := auth.token(t.Context(), 1)
		stuck <- err
	}()
	if id := <-s.arrived; id != "1" {
		t.Fatalf("first exchange is for installation %s, want 1", id)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if tok, err := auth.token(ctx, 2); err != nil || tok != "inst-2" {
		t.Errorf("installation 2 token = %q, %v; want inst-2 while installation 1 is stuck", tok, err)
	}
	<-s.arrived

	done, stop := context.WithCancel(t.Context())
	stop()
	if _, err := auth.token(done, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("waiting caller with a done ctx: err = %v, want context.Canceled", err)
	}

	close(release)
	if err := <-stuck; err != nil {
		t.Errorf("stuck exchange once released: %v", err)
	}
	if got := s.count("1"); got != 1 {
		t.Errorf("installation 1 exchanges = %d, want 1", got)
	}
}

// TestTokenWaiterOutlivesACancelledExchange: when the caller making an
// exchange gives up, a caller waiting on it gets a token of its own rather
// than the other caller's cancellation.
func TestTokenWaiterOutlivesACancelledExchange(t *testing.T) {
	release := make(chan struct{})
	s := &tokenServer{blocked: map[string]chan struct{}{"1": release}, arrived: make(chan string, 4)}
	auth := s.auth(t)

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leader := make(chan error, 1)
	go func() {
		_, err := auth.token(leaderCtx, 1)
		leader <- err
	}()
	<-s.arrived

	waiter := make(chan error, 1)
	go func() {
		tok, err := auth.token(t.Context(), 1)
		if err == nil && tok != "inst-1" {
			err = errors.New("wrong token " + tok)
		}
		waiter <- err
	}()
	cancelLeader()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled caller: err = %v, want context.Canceled", err)
	}
	<-s.arrived // the waiter's own exchange
	close(release)
	if err := <-waiter; err != nil {
		t.Errorf("waiter: %v, want its own token", err)
	}
}

// TestBearerAuthenticatesTheAPIOnly: the token goes to the API's scheme and
// host alone, and a redirect off the API does not carry it.
func TestBearerAuthenticatesTheAPIOnly(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = map[string]string{} // server to the Authorization it saw
	)
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[name] = r.Header.Get("Authorization")
			mu.Unlock()
		}
	}
	elsewhere := httptest.NewServer(record("elsewhere"))
	t.Cleanup(elsewhere.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/download", http.StatusFound)
	})
	mux.HandleFunc("/api/direct", record("api"))
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)

	auth := &appAuth{baseURL: api.URL + "/api/"}
	b, err := auth.bearer(func(context.Context) (string, error) { return "tok", nil }, http.DefaultTransport)
	if err != nil {
		t.Fatalf("bearer: %v", err)
	}
	client := &http.Client{Transport: b}
	for _, path := range []string{"/api/direct", "/api/redirect"} {
		resp, err := client.Get(api.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["api"] != "Bearer tok" {
		t.Errorf("the API saw Authorization %q, want the token", seen["api"])
	}
	if got, ok := seen["elsewhere"]; !ok || got != "" {
		t.Errorf("the redirect's target saw Authorization %q (reached: %v), want none", got, ok)
	}

	// Another scheme on the API's host is not the API either.
	other := api.URL[:len("http")] + "s" + api.URL[len("http"):] + "/api/direct"
	req := httptest.NewRequest(http.MethodGet, other, nil)
	var sent *http.Request
	b.base = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		sent = r
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
	})
	if _, err := b.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := sent.Header.Get("Authorization"); got != "" {
		t.Errorf("a request over https to the http API carried Authorization %q, want none", got)
	}
}

// roundTripperFunc is a function as an http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls f.
func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

// fakeRecorder is a Recorder in memory, ordering reports as a store
// shared by replicas would. Its calls can be made to fail.
type fakeRecorder struct {
	failRequest bool // RequestReview fails
	failOrder   bool // ReportInOrder cannot check the order

	order sync.Mutex // held across a report, as the store's lock is

	mu         sync.Mutex
	requested  map[prKey]int64 // pull request to its latest order
	reported   map[prKey]int64 // pull request to the latest order reported
	deliveries []DeliveryRecord
	starts     []ReviewStart
	ends       map[int64]ReviewEnd
	finished   chan int64
}

// prKey identifies a pull request, whatever its head.
type prKey struct {
	scope, repo string
	number      int
}

func keyOf(pr PullRequestInfo) prKey { return prKey{pr.Scope, pr.Repo, pr.Number} }

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{requested: map[prKey]int64{}, reported: map[prKey]int64{}, ends: map[int64]ReviewEnd{}, finished: make(chan int64, 16)}
}

var errRecorderDown = errors.New("recorder down")

func (f *fakeRecorder) RecordDelivery(_ context.Context, d DeliveryRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries = append(f.deliveries, d)
	return nil
}

func (f *fakeRecorder) RequestReview(_ context.Context, pr PullRequestInfo) (int64, error) {
	key := keyOf(pr)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRequest {
		return 0, errRecorderDown
	}
	f.requested[key]++
	return f.requested[key], nil
}

func (f *fakeRecorder) StartReview(_ context.Context, r ReviewStart) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, r)
	return int64(len(f.starts)), nil
}

func (f *fakeRecorder) ReportInOrder(ctx context.Context, pr PullRequestInfo, order int64, report func(context.Context) error) (bool, error) {
	key := keyOf(pr)
	if f.failOrder {
		return false, errRecorderDown
	}
	f.order.Lock()
	defer f.order.Unlock()
	f.mu.Lock()
	newer := f.reported[key] >= order
	f.mu.Unlock()
	if newer {
		return false, nil
	}
	if err := report(ctx); err != nil {
		return true, err
	}
	f.mu.Lock()
	f.reported[key] = order
	f.mu.Unlock()
	return true, nil
}

func (f *fakeRecorder) StartInOrder(ctx context.Context, pr PullRequestInfo, order int64, start func(context.Context) error) (bool, error) {
	if f.failOrder {
		return false, errRecorderDown
	}
	f.order.Lock()
	defer f.order.Unlock()
	f.mu.Lock()
	newer := f.reported[keyOf(pr)] >= order
	f.mu.Unlock()
	if newer {
		return false, nil
	}
	return true, start(ctx)
}

func (f *fakeRecorder) FinishReview(_ context.Context, id int64, r ReviewEnd) error {
	f.mu.Lock()
	f.ends[id] = r
	f.mu.Unlock()
	f.finished <- id
	return nil
}

// end waits for a review to finish and returns its record.
func (f *fakeRecorder) end(t *testing.T) ReviewEnd {
	t.Helper()
	select {
	case id := <-f.finished:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.ends[id]
	case <-time.After(5 * time.Second):
		t.Fatal("no review finished")
		return ReviewEnd{}
	}
}

// approvedPullRequest is a stub pull request whose review passes.
func approvedPullRequest() *stubPullRequest {
	c := newStubPullRequest()
	c.key = "o/r@head"
	c.input = approval.ReviewInput{Author: "dev", Approvers: []string{"lead"}}
	return c
}

// TestServerRecordsDeliveriesAndReviews: every verified delivery is
// recorded with what became of it, and a review with its order, input,
// state, targets, and outcome.
func TestServerRecordsDeliveriesAndReviews(t *testing.T) {
	rec := newFakeRecorder()
	opts := testOptions()
	opts.Recorder = rec
	c := approvedPullRequest()
	s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	for event, want := range map[string]int{"pr": http.StatusAccepted, "ping": http.StatusAccepted, "malformed": http.StatusBadRequest} {
		if code, _ := post(t, s, "/webhooks/stub", event, "ok"); code != want {
			t.Fatalf("%s webhook = %d, want %d", event, code, want)
		}
	}
	end := rec.end(t)
	s.records.Wait()

	if end.Status != ReviewReported || end.Outcome.Verdict != approval.Passed {
		t.Errorf("review ended %s with %s, want reported and passed", end.Status, end.Outcome.Verdict)
	}
	if end.Input == nil || end.State == nil || end.Err != "" {
		t.Errorf("review record input %v, state %v, err %q; want the input and state, and no error", end.Input, end.State, end.Err)
	}
	if want := []TargetRecord{{Target: "lead", Resolved: true}}; !reflect.DeepEqual(end.Targets, want) {
		t.Errorf("targets = %+v, want %+v", end.Targets, want)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.starts) != 1 || rec.starts[0].Order != 1 || rec.starts[0].DeliveryID != "delivery-pr" || rec.starts[0].PullRequest.Repo != "o/r" {
		t.Errorf("starts = %+v, want one at order 1 from delivery-pr for o/r", rec.starts)
	}
	got := map[string]string{}
	for _, d := range rec.deliveries {
		got[d.ID] = d.Disposition
		if d.Disposition == DeliveryQueued && (d.PullRequest == nil || d.PullRequest.Number != 1) {
			t.Errorf("queued delivery %+v names no pull request", d)
		}
	}
	want := map[string]string{"delivery-pr": DeliveryQueued, "delivery-ping": DeliveryIgnored, "delivery-malformed": DeliveryUnusable}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deliveries = %v, want %v", got, want)
	}
}

// TestReviewSupersededOnAnotherReplicaDoesNotReport: a review whose key a
// newer review, on another replica, already reported publishes nothing:
// neither that it runs nor its outcome.
func TestReviewSupersededOnAnotherReplicaDoesNotReport(t *testing.T) {
	rec := newFakeRecorder()
	rec.reported[prKey{"stub", "o/r", 1}] = 100
	opts := testOptions()
	opts.Recorder = rec
	c := approvedPullRequest()
	s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusAccepted {
		t.Fatalf("webhook = %d, want 202", code)
	}
	if end := rec.end(t); end.Status != ReviewSuperseded {
		t.Errorf("review ended %s, want superseded", end.Status)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) != 0 || c.starts != 0 {
		t.Errorf("reports = %+v and %d starts, want none", c.reports, c.starts)
	}
}

// TestUnorderedReviewNeverPasses: a review whose order among the replicas'
// reports is unknown, because its request or its report could not be
// recorded, reports anything but a pass as is and a pass as errored.
func TestUnorderedReviewNeverPasses(t *testing.T) {
	tests := []struct {
		name        string
		failRequest bool
		failOrder   bool
		approved    bool
		want        approval.Verdict
	}{
		{name: "request not recorded, passing", failRequest: true, approved: true, want: approval.Errored},
		{name: "order not checked, passing", failOrder: true, approved: true, want: approval.Errored},
		{name: "order not checked, pending", failOrder: true, want: approval.Pending},
		{name: "ordered, passing", approved: true, want: approval.Passed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newFakeRecorder()
			rec.failRequest, rec.failOrder = tt.failRequest, tt.failOrder
			opts := testOptions()
			opts.Recorder = rec
			c := approvedPullRequest()
			if !tt.approved {
				c.input.Approvers = nil
			}
			s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
			if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusAccepted {
				t.Fatalf("webhook = %d, want 202", code)
			}
			c.wait(t)
			end := rec.end(t)
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.reports) != 1 || c.reports[0].Verdict != tt.want {
				t.Fatalf("reports = %+v, want one %s", c.reports, tt.want)
			}
			if tt.want == approval.Errored && c.reports[0].Summary != unorderedSummary {
				t.Errorf("summary = %q, want %q", c.reports[0].Summary, unorderedSummary)
			}
			if end.Outcome.Verdict != tt.want {
				t.Errorf("recorded outcome %s, want the published %s", end.Outcome.Verdict, tt.want)
			}
		})
	}
}

package reviewview

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// testState is a review of two files: one approved, one missing an
// approval, with an error naming the second and a hostile path.
func testState() *approval.ReviewState {
	target := func(name string, status approval.Status, by ...string) approval.ApproverTargetState {
		return approval.ApproverTargetState{
			ApproverTarget: rulesfile.ApproverTarget{Target: name, RequiredCount: 1},
			SatisfiedCount: len(by), ResolvedBy: by, Status: status,
		}
	}
	file := func(path string, status approval.Status, t approval.ApproverTargetState) approval.FileState {
		return approval.FileState{
			Path:   path,
			Status: status,
			Layers: []approval.LayerState{{
				DirectoryPath: "",
				Status:        status,
				Rules: []approval.RuleState{{
					Rule:      rulesfile.Rule{Pattern: `.*\.go`, Name: "go"},
					Approvers: []approval.ApproverTargetState{t},
					Status:    status,
				}},
			}},
			Required: []approval.ApproverTargetState{t},
		}
	}
	return &approval.ReviewState{
		Files: map[string]approval.FileState{
			"cmd/main.go":                   file("cmd/main.go", approval.StatusSatisfied, target("backend", approval.StatusSatisfied, "alice")),
			"web/<script>alert(1)</script>": file("web/<script>alert(1)</script>", approval.StatusMissingApprovers, target("security", approval.StatusMissingApprovers)),
		},
		Overall: approval.StatusMissingApprovers,
		Events: []approval.Event{
			{Phase: "fetch", Status: approval.StatusDone, Note: "fetched rules"},
			{Phase: "approve", Path: "cmd/main.go", Status: approval.StatusSatisfied},
		},
		Errors: []string{"resolve team for web/<script>alert(1)</script>: timeout"},
	}
}

func decodeTestState(t *testing.T) *Report {
	t.Helper()
	data, err := testState().MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	r, err := DecodeReport(data)
	if err != nil {
		t.Fatalf("DecodeReport: %v", err)
	}
	return r
}

func render(t *testing.T, p Page) string {
	t.Helper()
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.String()
}

// TestDecodeReportReadsTheRecordedJSON: every part of the review's JSON
// the page shows decodes.
func TestDecodeReportReadsTheRecordedJSON(t *testing.T) {
	r := decodeTestState(t)
	if r.Overall != "missing_approvers" || len(r.Files) != 2 || len(r.Errors) != 1 || len(r.Events) != 2 {
		t.Fatalf("report = %+v, want the state's overall, 2 files, 1 error, 2 events", r)
	}
	f := r.Files[0] // most in need of attention first
	if f.Status != "missing_approvers" || len(f.Layers) != 1 || f.Layers[0].RulesFile == "" {
		t.Errorf("first file = %+v, want the one missing approvals, with its rules file", f)
	}
	rule := f.Layers[0].Rules[0]
	if rule.Pattern != `.*\.go` || rule.Name != "go" || rule.Strategy == "" || len(rule.Approvers) != 1 {
		t.Errorf("rule = %+v, want its pattern, name, strategy, and target", rule)
	}
	approved := r.Files[1].Required[0]
	if approved.Target != "backend" || approved.RequiredCount != 1 || approved.Status != "satisfied" || len(approved.ApprovedBy) != 1 {
		t.Errorf("approved target = %+v, want backend approved by alice", approved)
	}
}

// TestRenderFinishedReport: the report escapes the review's data, links
// only to IDs it defines, and loads nothing from elsewhere.
func TestRenderFinishedReport(t *testing.T) {
	out := render(t, Page{
		Repo: "o/r", Number: 7, HeadSHA: strings.Repeat("a", 40), State: StateFinished, Status: "reported",
		Outcome:    approval.Outcome{Verdict: approval.Pending, Summary: "1 of 2 files await approval", Items: []string{"security"}},
		FinishedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Report:     decodeTestState(t),
	})
	if strings.Contains(out, "<script>alert(1)") {
		t.Error("the page carries a path's markup unescaped")
	}
	for _, want := range []string{"o/r #7", "1 of 2 files await approval", "cmd/main.go", "backend", "2026-01-02 03:04:05 UTC"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(out, "data-live") {
		t.Error("a finished review's page polls for progress")
	}
	assertSelfContained(t, out)
	assertLinksResolve(t, out)
}

// TestRenderLiveStates: a page whose review has not finished polls its
// progress URL and shows how far the review has come.
func TestRenderLiveStates(t *testing.T) {
	tests := []struct {
		name string
		page Page
		want []string
		not  []string
	}{
		{
			name: "pending",
			page: Page{State: StatePending},
			want: []string{"No review yet", `data-state="pending"`},
			not:  []string{`id="live-stages"`},
		},
		{
			name: "queued",
			page: Page{State: StateQueued, Stage: "queued"},
			want: []string{"Review queued", `class="stage current"><span class="stage-mark" aria-hidden="true">●</span><span>Waiting for a worker`},
		},
		{
			name: "approving",
			page: Page{State: StateRunning, Stage: "approve", Done: 3, Total: 10},
			want: []string{"Review running", "3 of 10 files", `class="stage done"><span class="stage-mark" aria-hidden="true">✓</span><span>Resolving approver targets`},
		},
		{
			name: "on another server",
			page: Page{State: StateRunning},
			want: []string{"Another Canopy server runs this review"},
			not:  []string{"stage current"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.page.Repo, tt.page.Number, tt.page.HeadSHA = "o/r", 1, strings.Repeat("b", 40)
			tt.page.ProgressURL = "1/progress?sig=x"
			out := render(t, tt.page)
			for _, want := range append(tt.want, "data-live", `data-progress-url="1/progress?sig=x"`, `http-equiv="refresh"`) {
				if !strings.Contains(out, want) {
					t.Errorf("the page lacks %q", want)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(out, not) {
					t.Errorf("the page has %q", not)
				}
			}
			assertSelfContained(t, out)
		})
	}
}

// TestRenderCapsEvents: a review with more events than a browser can show
// shows the first maxEvents and says how many more there are.
func TestRenderCapsEvents(t *testing.T) {
	r := &Report{Overall: "satisfied", Events: make([]Event, maxEvents+5)}
	out := render(t, Page{Repo: "o/r", Number: 1, HeadSHA: "c", State: StateFinished, Report: r})
	if n := strings.Count(out, `<article class="event"`); n != maxEvents {
		t.Errorf("events shown = %d, want %d", n, maxEvents)
	}
	if !strings.Contains(out, "5 later events are not shown") {
		t.Error("the page does not say how many events it leaves out")
	}
}

func TestMentionsPath(t *testing.T) {
	tests := []struct {
		text, path string
		want       bool
	}{
		{"read src/main.go: denied", "src/main.go", true},
		{`open "/repo/src/main.go"`, "src/main.go", true},
		{"read src/main.go.old: denied", "src/main.go", false},
		{"read main.go: denied", "src/main.go", false},
		{"a.go", "a.go", false}, // too short to link confidently
	}
	for _, tt := range tests {
		if got := mentionsPath(tt.text, tt.path); got != tt.want {
			t.Errorf("mentionsPath(%q, %q) = %v, want %v", tt.text, tt.path, got, tt.want)
		}
	}
}

// assertSelfContained checks the page loads no resource from elsewhere.
func assertSelfContained(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"<link", "src=", "@import", "url(http", "//fonts."} {
		if strings.Contains(out, bad) {
			t.Errorf("the page references an outside resource: %q", bad)
		}
	}
}

var (
	idAttr   = regexp.MustCompile(` id="([^"]+)"`)
	fragment = regexp.MustCompile(`href="#([^"]+)"`)
)

// assertLinksResolve checks every ID is unique and every in-page link
// names one.
func assertLinksResolve(t *testing.T, out string) {
	t.Helper()
	ids := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(out, -1) {
		if ids[m[1]] {
			t.Errorf("duplicate id %q", m[1])
		}
		ids[m[1]] = true
	}
	for _, m := range fragment.FindAllStringSubmatch(out, -1) {
		if !ids[m[1]] {
			t.Errorf("link to #%s, which no element has", m[1])
		}
	}
}

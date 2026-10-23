// Package reviewview renders a review page: a self-contained HTML page
// showing a pull request's newest review, live while it is queued or
// running, then in full, from its file → layer → rule → approver target
// decision paths to its errors and event trace.
//
// The page renders a review from its recorded JSON (see
// approval.ReviewState.MarshalJSON), so a review read back from the
// database and one this process just ran render alike. It inlines its CSS
// and JavaScript and loads nothing else, and works without JavaScript
// except for the live updates.
package reviewview

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"path"
	"strings"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

//go:embed review.tmpl
var reviewTemplate string

var tmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"add":          func(a, b int) int { return a + b },
	"join":         strings.Join,
	"statusClass":  statusClass,
	"statusLabel":  statusLabel,
	"verdictClass": verdictClass,
	"isIssue":      func(status string) bool { return status != "satisfied" },
	"openStatus":   openStatus,
	"shortSHA":     shortSHA,
	"timestamp":    timestamp,
}).Parse(reviewTemplate))

// The states of a review page.
const (
	StatePending  = "pending"  // no review of the commit is known yet
	StateQueued   = "queued"   // a review waits for a worker
	StateRunning  = "running"  // a review runs
	StateFinished = "finished" // the newest review ended
)

// Page is a review page: a pull request's head commit and its newest
// review, in whatever state it is.
type Page struct {
	Repo    string // "owner/name"
	Number  int
	HeadSHA string
	// State is one of the State* constants.
	State string
	// Stage is the running review's stage: "queued", an
	// approval.Progress phase, or "report"; "" when it is not known, as
	// for a review another server runs. Done and Total count the files
	// approved so far.
	Stage       string
	Done, Total int
	QueuedAt    time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	// Status is how a finished review ended, e.g. "reported" or
	// "superseded".
	Status string
	// Outcome is a finished review's outcome.
	Outcome approval.Outcome
	// Report is a finished review's state; nil when it is not available.
	Report *Report
	// ProgressURL is the URL the page polls while the review is not
	// finished, relative to the page.
	ProgressURL string
}

// Render writes p as a complete HTML document.
func Render(w io.Writer, p Page) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, newPageView(p)); err != nil {
		return fmt.Errorf("render review page: %w", err)
	}
	_, err := buf.WriteTo(w)
	return err
}

// Report is a review's state as recorded: the JSON form
// approval.ReviewState.MarshalJSON encodes.
type Report struct {
	Overall  string   `json:"overall"`
	Files    []File   `json:"files"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Events   []Event  `json:"events"`
}

// File is one file's decision path.
type File struct {
	Path         string   `json:"path"`
	Status       string   `json:"status"`
	UsedFallback bool     `json:"used_fallback"`
	Required     []Target `json:"required"`
	Layers       []Layer  `json:"layers"`
}

// Layer is one rules file's part in a file's decision.
type Layer struct {
	RulesFile string        `json:"rules_file"`
	Status    string        `json:"status"`
	Rules     []Rule        `json:"rules"`
	Requires  []Requirement `json:"requires"`
}

// Rule is one rule's part in a layer.
type Rule struct {
	Pattern   string   `json:"pattern"`
	Name      string   `json:"name"`
	Strategy  string   `json:"combine_strategy"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason"`
	Approvers []Target `json:"approvers"`
}

// Requirement is an approver target and how many approvals it owes.
type Requirement struct {
	Target        string `json:"target"`
	RequiredCount int    `json:"required_count"`
}

// Target is an evaluated approver target.
type Target struct {
	Requirement
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	ApprovedBy []string `json:"approved_by"`
}

// Event is one step of the review's trace.
type Event struct {
	Phase     string `json:"phase"`
	Path      string `json:"path"`
	RulesFile string `json:"rules_file"`
	Rule      string `json:"rule"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// DecodeReport decodes a review's recorded JSON.
func DecodeReport(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode review state: %w", err)
	}
	return &r, nil
}

// maxEvents is how many of a review's events the page shows: a large
// review records millions, more than a browser can show.
const maxEvents = 2000

// pageView is a Page as the template shows it.
type pageView struct {
	Page
	Live   bool // the page updates itself until the review finishes
	Stages []stageView
	Report *reportView
}

// stageView is one stage of a running review.
type stageView struct {
	Label string
	State string // "done", "current", or "todo"
	Note  string
}

// liveStages are a review's stages in order, with what each does.
var liveStages = []struct{ stage, label string }{
	{"queued", "Waiting for a worker"},
	{"change", "Reading the pull request"},
	{"fetch", "Fetching rules files"},
	{"sift", "Matching rules to files"},
	{"resolve", "Resolving approver targets"},
	{"approve", "Evaluating approvals"},
	{"report", "Publishing the check"},
}

func newPageView(p Page) pageView {
	v := pageView{Page: p, Live: p.State != StateFinished}
	if v.Live {
		v.Stages = stagesOf(p)
	}
	if p.Report != nil {
		v.Report = newReportView(p.Report)
	}
	return v
}

// stagesOf is p's stages, marked done up to its current one. A review of
// unknown stage has none current.
func stagesOf(p Page) []stageView {
	current := -1
	for i, s := range liveStages {
		if s.stage == p.Stage {
			current = i
		}
	}
	out := make([]stageView, len(liveStages))
	for i, s := range liveStages {
		out[i] = stageView{Label: s.label, State: "todo"}
		switch {
		case i < current:
			out[i].State = "done"
		case i == current:
			out[i].State = "current"
			if s.stage == "approve" && p.Total > 0 {
				out[i].Note = fmt.Sprintf("%d of %d files", p.Done, p.Total)
			}
		}
	}
	return out
}

// reportView is a Report as the template shows it, cross-linked.
type reportView struct {
	Overall                 string
	Files                   []fileView
	Errors                  []errorView
	Warnings                []string
	Events                  []eventView // at most maxEvents
	EventCount              int
	HiddenEvents            int
	FilesNeedingAttention   int
	TargetsNeedingAttention int
}

type fileView struct {
	File
	ID         string
	Index      int
	Errors     []linkView // errors naming the file
	FirstEvent string     // the ID of the file's first shown event; "" for none
	EventCount int        // the file's shown events
}

type linkView struct {
	ID    string
	Index int
	Label string
}

type errorView struct {
	ID    string
	Index int
	Text  string
	Files []linkView // files the error names by full path
}

type eventView struct {
	Event
	ID     string
	Index  int
	FileID string // the event's file; "" for none
}

func newReportView(r *Report) *reportView {
	v := &reportView{Overall: r.Overall, Warnings: r.Warnings, EventCount: len(r.Events)}
	byPath := make(map[string]int, len(r.Files))
	for i, f := range r.Files {
		fv := fileView{File: f, ID: fileID(f.Path), Index: i + 1}
		v.Files = append(v.Files, fv)
		byPath[normalizePath(f.Path)] = i
		if f.Status != "satisfied" {
			v.FilesNeedingAttention++
		}
		for _, t := range f.Required {
			if t.Status != "satisfied" {
				v.TargetsNeedingAttention++
			}
		}
	}
	for i, text := range r.Errors {
		ev := errorView{ID: fmt.Sprintf("err-%03d", i+1), Index: i + 1, Text: text}
		for j := range v.Files {
			f := &v.Files[j]
			if mentionsPath(text, f.Path) {
				ev.Files = append(ev.Files, linkView{ID: f.ID, Label: f.Path})
				f.Errors = append(f.Errors, linkView{ID: ev.ID, Index: ev.Index})
			}
		}
		v.Errors = append(v.Errors, ev)
	}
	shown := r.Events[:min(len(r.Events), maxEvents)]
	v.HiddenEvents = len(r.Events) - len(shown)
	for i, e := range shown {
		ev := eventView{Event: e, ID: fmt.Sprintf("event-%04d", i+1), Index: i + 1}
		if j, ok := byPath[normalizePath(e.Path)]; ok && e.Path != "" {
			f := &v.Files[j]
			ev.FileID = f.ID
			if f.FirstEvent == "" {
				f.FirstEvent = ev.ID
			}
			f.EventCount++
		}
		v.Events = append(v.Events, ev)
	}
	return v
}

// fileID is a stable element ID for a file's details.
func fileID(p string) string {
	sum := sha256.Sum256([]byte(p))
	return "file-" + hex.EncodeToString(sum[:8])
}

// mentionsPath reports whether text names the file at p by its full path.
// A basename alone does not count: linking an error to the wrong file
// would mislead more than leaving it unlinked.
func mentionsPath(text, p string) bool {
	p = strings.ToLower(normalizePath(p))
	if len(p) < 5 {
		return false
	}
	text = strings.ToLower(strings.ReplaceAll(text, `\`, "/"))
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], p)
		if i < 0 {
			return false
		}
		start, end := offset+i, offset+i+len(p)
		// Delimited, so src/main.go does not match src/main.go.old.
		if (start == 0 || strings.ContainsRune(" \t\r\n\"'`([{=,:;/", rune(text[start-1]))) &&
			(end == len(text) || strings.ContainsRune(" \t\r\n\"'`)]},;:", rune(text[end]))) {
			return true
		}
		offset = start + 1
	}
	return false
}

func normalizePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	if p == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean(p), "./")
}

// statusClass is the CSS class suffix of a status: one of the statuses
// the stylesheet colors, or "unknown".
func statusClass(status string) string {
	switch status {
	case "unresolved", "error", "satisfied", "missing_approvers", "active", "inactive", "done":
		return status
	}
	return "unknown"
}

// statusLabel is a status as a person reads it: "missing_approvers" is
// "Missing approvers".
func statusLabel(status string) string {
	if status == "" {
		return "Unknown"
	}
	s := strings.ReplaceAll(status, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// verdictClass is the status class colored like verdict.
func verdictClass(v approval.Verdict) string {
	switch v {
	case approval.Passed:
		return "satisfied"
	case approval.Pending:
		return "missing_approvers"
	case approval.Failed, approval.Errored:
		return "error"
	}
	return "unknown"
}

// openStatus reports whether a node of status starts expanded: it needs
// attention.
func openStatus(status string) bool {
	return status == "error" || status == "unresolved" || status == "missing_approvers"
}

func shortSHA(sha string) string { return sha[:min(len(sha), 12)] }

func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

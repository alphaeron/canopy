package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alphaeron/canopy/internal/reviewview"
)

// reviewPageInput names a review page, with its link's expiry and
// signature.
type reviewPageInput struct {
	Owner  string `path:"owner" maxLength:"100" doc:"The repository's owner."`
	Name   string `path:"name" maxLength:"100" doc:"The repository's name."`
	Number int    `path:"number" minimum:"1" doc:"The pull request's number."`
	SHA    string `path:"sha" pattern:"^([0-9a-f]{40}|[0-9a-f]{64})$" doc:"The pull request's head commit."`
	Exp    int64  `query:"exp" required:"true" minimum:"1" doc:"When the link expires, in Unix seconds, as the pull request's status carries it."`
	Sig    string `query:"sig" required:"true" maxLength:"64" doc:"The link's signature, as the pull request's status carries it."`
}

// ref is the page in, once its signature verifies.
func (in *reviewPageInput) ref(l *links) (pageRef, error) {
	ref := pageRef{repo: strings.ToLower(in.Owner + "/" + in.Name), number: in.Number, headSHA: in.SHA}
	if !l.verify(ref.repo, ref.number, ref.headSHA, in.Exp, in.Sig) {
		// Not found rather than forbidden, for an expired link too: an
		// unsigned request learns nothing about which reviews exist.
		return pageRef{}, huma.Error404NotFound("review page not found")
	}
	return ref, nil
}

// reviewPageOutput is a review page's HTML.
type reviewPageOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	// The page inlines its styles and scripts and loads nothing else; it
	// polls only its own progress.
	ContentSecurityPolicy string `header:"Content-Security-Policy"`
	// The link's signature is in the page's URL: no referrer carries it
	// anywhere.
	ReferrerPolicy string `header:"Referrer-Policy"`
	Body           []byte
}

// progressOutput is how far a review page's review has come.
type progressOutput struct {
	CacheControl   string `header:"Cache-Control"`
	ReferrerPolicy string `header:"Referrer-Policy"`
	Body           struct {
		State string `json:"state" enum:"pending,queued,running,finished" doc:"The newest review's state: pending when none is known yet."`
		Stage string `json:"stage,omitempty" doc:"The running review's stage: queued, change, fetch, sift, resolve, approve, or report; empty when another server runs it."`
		Done  int    `json:"done" doc:"Files approved so far, in the approve stage."`
		Total int    `json:"total" doc:"Files to approve, in the approve stage."`
	}
}

const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// registerReviewPages registers the review page and its progress, which
// the page polls while its review is not finished.
func (s *Server) registerReviewPages(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "review-page",
		Method:      http.MethodGet,
		Path:        ReviewPagePath,
		Summary:     "Show a pull request's review",
		Description: "The newest review of a pull request's head commit, as an HTML page: its progress while it is queued " +
			"or running, then its outcome and full decision path. The pull request's status links to it; a link the server " +
			"did not sign, or one that has expired, is not found.",
		Tags:   []string{"reviews"},
		Errors: []int{http.StatusNotFound, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *reviewPageInput) (*reviewPageOutput, error) {
		ref, err := in.ref(s.links)
		if err != nil {
			return nil, err
		}
		page, err := s.page(ctx, ref, true)
		if err != nil {
			return nil, err
		}
		page.ProgressURL = in.SHA + "/progress?" + linkQuery(in.Exp, in.Sig)
		var body bytes.Buffer
		if err := reviewview.Render(&body, page); err != nil {
			s.log.Error("rendering a review page failed", "component", "server", "repo", ref.repo, "number", ref.number, "error", err)
			return nil, huma.Error500InternalServerError("review page could not be rendered")
		}
		return &reviewPageOutput{
			ContentType:           "text/html; charset=utf-8",
			CacheControl:          "no-store",
			ContentSecurityPolicy: pageCSP,
			ReferrerPolicy:        "no-referrer",
			Body:                  body.Bytes(),
		}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "review-progress",
		Method:      http.MethodGet,
		Path:        ReviewPagePath + "/progress",
		Summary:     "Follow a pull request's review",
		Description: "How far the newest review of a pull request's head commit has come, for its review page to poll. " +
			"It takes the review page's expiry and signature.",
		Tags:   []string{"reviews"},
		Errors: []int{http.StatusNotFound, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *reviewPageInput) (*progressOutput, error) {
		ref, err := in.ref(s.links)
		if err != nil {
			return nil, err
		}
		page, err := s.page(ctx, ref, false)
		if err != nil {
			return nil, err
		}
		out := &progressOutput{CacheControl: "no-store", ReferrerPolicy: "no-referrer"}
		out.Body.State, out.Body.Stage, out.Body.Done, out.Body.Total = page.State, page.Stage, page.Done, page.Total
		return out, nil
	})
}

// page is ref's review page: its newest review, from this server's own
// reviews and the recorded ones. A review this server is running is
// newest; then a finished recorded one; then one this server finished
// (whose end may not have been recorded); then one another server is
// running. With full, a finished review's state is decoded too.
func (s *Server) page(ctx context.Context, ref pageRef, full bool) (reviewview.Page, error) {
	log := s.log.With("component", "server", "repo", ref.repo, "number", ref.number, "head", ref.headSHA)
	p := reviewview.Page{Repo: ref.repo, Number: ref.number, HeadSHA: ref.headSHA, State: reviewview.StatePending}
	live, hasLive := s.live.get(ref)
	var rec *ReviewRecord
	if s.opts.History != nil {
		hctx, cancel := context.WithTimeout(ctx, recordTimeout)
		var err error
		rec, err = s.opts.History.LatestReview(hctx, ref.repo, ref.number, ref.headSHA)
		cancel()
		if err != nil {
			log.Warn("reading the review record failed", "error", err)
			if !hasLive {
				return p, huma.Error503ServiceUnavailable("review record unavailable")
			}
			rec = nil
		}
	}
	// The same review when it was queued at the same time; the record
	// keeps microseconds.
	sameReview := rec != nil && hasLive && rec.QueuedAt.Sub(live.QueuedAt).Abs() < time.Millisecond

	switch {
	case hasLive && live.Stage != stageFinished:
		p.State = reviewview.StateRunning
		if live.Stage == stageQueued {
			p.State = reviewview.StateQueued
		}
		p.Stage, p.Done, p.Total = live.Stage, live.Done, live.Total
		p.QueuedAt, p.StartedAt = live.QueuedAt, live.StartedAt
	case rec != nil && rec.Status != ReviewRunning:
		p.State, p.Status, p.Outcome = reviewview.StateFinished, rec.Status, rec.Outcome
		p.QueuedAt, p.StartedAt, p.FinishedAt = rec.QueuedAt, rec.StartedAt, rec.FinishedAt
		if full && rec.State != nil {
			p.Report = decodeReport(rec.State, log)
		}
	case hasLive && (rec == nil || sameReview):
		p.State, p.Status, p.Outcome = reviewview.StateFinished, live.Status, live.Outcome
		p.QueuedAt, p.StartedAt, p.FinishedAt = live.QueuedAt, live.StartedAt, live.FinishedAt
		if full && live.State != nil {
			if data, err := json.Marshal(live.State); err != nil {
				log.Warn("encoding the review state failed", "error", err)
			} else {
				p.Report = decodeReport(data, log)
			}
		}
	case rec != nil:
		p.State = reviewview.StateRunning
		p.QueuedAt, p.StartedAt = rec.QueuedAt, rec.StartedAt
	}
	return p, nil
}

// decodeReport is the review state in data, or nil, logged, when it does
// not decode: the page then shows the outcome alone.
func decodeReport(data []byte, log *slog.Logger) *reviewview.Report {
	r, err := reviewview.DecodeReport(data)
	if err != nil {
		log.Warn("decoding the recorded review state failed", "error", err)
		return nil
	}
	return r
}

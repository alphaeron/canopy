// Package server is the review server: it implements the pull request
// webhook flow. A platform's PRWebhook receives the platform's webhooks and
// names the PullRequest each event affects; the server reviews that pull
// request in a bounded worker pool and has it report the outcome as its
// status. It is platform-agnostic: everything particular to a platform is
// behind PRWebhook and PullRequest. Routes are registered with huma on a chi router, so
// the OpenAPI document and its docs page are generated from them.
package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/logx"
	"github.com/alphaeron/canopy/internal/parallel"
	"github.com/alphaeron/canopy/internal/review"
)

// Options configure a Server.
type Options struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// Workers is how many reviews run at once.
	Workers int
	// Queue is how many pull requests (by PullRequest.Key) may wait for a
	// worker; a webhook for a pull request already waiting or under review
	// takes no more room. One arriving when the queue is full is refused
	// with 503.
	Queue int
	// ReviewTimeout bounds each review.
	ReviewTimeout time.Duration
	// ReportTimeout bounds reporting the outcome of a review that ran out
	// of time, which can no longer use the review's own context.
	ReportTimeout time.Duration
	// ShutdownTimeout bounds how long ListenAndServe waits for open
	// requests once its context is done.
	ShutdownTimeout time.Duration
	// Version is the server's version, shown in the OpenAPI document.
	Version string
	// Log receives the server's structured logs; nil means slog.Default().
	Log *slog.Logger
	// Recorder records webhooks and reviews and orders reports across
	// replicas of the server; nil records nothing, and only this process's
	// reports are ordered.
	Recorder Recorder
	// History reads recorded reviews back for review pages; nil shows only
	// the reviews this server queued or ran recently.
	History ReviewHistory
	// PublicURL is the server's URL as the platform's users reach it, e.g.
	// "https://canopy.example.com". With LinkKey, it serves review pages
	// (ReviewPagePath), and each pull request's status links to its own.
	// Both empty serve none.
	PublicURL string
	// LinkKey signs the links to review pages: at least MinLinkKeyBytes
	// long and the same on every replica. Changing it breaks every link
	// published before.
	LinkKey []byte
	// LinkLifetime is how long a review page's link opens its page after
	// the server signs it: at least MinLinkLifetime when links are served.
	// A link leaked from a status, a log, or a screenshot stops working, as
	// does one held by someone who has since lost access to the repository.
	LinkLifetime time.Duration
	// BlockingLabels are the labels that fail a pull request carrying any
	// of them, whatever its approvals (see review.Evaluate); compared
	// case-insensitively. Empty blocks on no label.
	BlockingLabels []string
}

// Bounds on recording: how long the calls a webhook or a review waits for
// may take, and how many records are written at once in the background
// (more are dropped) and for how long each.
const (
	recordTimeout       = 2 * time.Second
	maxRecordWrites     = 16
	recordWriteTimeout  = 10 * time.Second
	maxDeliveryIDLength = 128
)

// Queued statuses (see markQueued): how long a review waits for a worker
// before its pull request shows it queued, how many wait or publish at once
// (more are skipped, as a queued status only shows the review waiting), and
// how long each may take. A worker that is free starts a review well within
// the delay, so an idle server spends no request on it.
const (
	queuedMarkDelay   = 500 * time.Millisecond
	maxQueuedMarks    = 64
	queuedMarkTimeout = 10 * time.Second
)

// Bounds on prefetching (see Prefetcher): how many prefetches run at once
// (more are dropped, as each only warms caches) and for how long each.
const (
	maxPrefetches   = 4
	prefetchTimeout = 2 * time.Minute
)

// Bounds on each HTTP connection. A webhook's whole body is read before its
// signature can be checked, so without readTimeout an unauthenticated
// client sending its body slowly would hold the connection and its buffer
// indefinitely. Every response is written as soon as it is ready (a review
// runs after its webhook is answered), so writeTimeout is generous.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 2 * time.Minute
)

// unorderedSummary is the outcome a review reports instead of passing when
// it could not be ordered with the other replicas' reports.
const unorderedSummary = "review could not be recorded; see the server logs"

// Paths the server serves besides the webhooks.
const (
	// OpenAPIPath is the OpenAPI document, without its extension: huma
	// serves it as .json and .yaml.
	OpenAPIPath = "/openapi"
	// DocsPath is the interactive API documentation.
	DocsPath = "/swagger"
)

// Server is the review server. Create it with New.
type Server struct {
	opts    Options
	log     *slog.Logger
	handler http.Handler
	reviews *reviews
	records *parallel.Background // nil without a Recorder
	marks   *parallel.Background // publishes queued statuses
	fetches *parallel.Background // prefetches (see Prefetcher)
	ctx     context.Context      // the server's lifetime, as New was given
	links   *links               // nil without review pages
	live    *tracker
}

// New builds the server over the deployment's one platform webhook and
// starts its review workers. Reviews run under ctx, so cancelling it
// cancels them; Wait waits for the workers to stop.
func New(ctx context.Context, opts Options, webhook PRWebhook) (*Server, error) {
	if webhook == nil {
		return nil, errors.New("no platform to serve")
	}
	if opts.Workers <= 0 || opts.Queue <= 0 || opts.ReviewTimeout <= 0 || opts.ReportTimeout <= 0 || opts.ShutdownTimeout <= 0 {
		return nil, fmt.Errorf("workers, queue, review timeout, report timeout, and shutdown timeout must be positive, got %d, %d, %s, %s, %s",
			opts.Workers, opts.Queue, opts.ReviewTimeout, opts.ReportTimeout, opts.ShutdownTimeout)
	}
	s := &Server{opts: opts, log: logx.OrDefault(opts.Log), live: newTracker(opts.History == nil)}
	var err error
	if s.links, err = newLinks(opts.PublicURL, opts.LinkKey, opts.LinkLifetime); err != nil {
		return nil, err
	}

	router := chi.NewMux()
	cfg := huma.DefaultConfig(brand.Name+" review server", opts.Version)
	cfg.Info.Description = "Receives hosted platforms' pull request webhooks and reviews each pull request against its " +
		"base revision's " + brand.RulesFileName + " rules, reporting the outcome as the pull request's status."
	cfg.OpenAPIPath = OpenAPIPath
	cfg.DocsPath = DocsPath
	cfg.DocsRenderer = huma.DocsRendererSwaggerUI
	api := humachi.New(router, cfg)

	name, path := webhook.Name(), webhook.WebhookPath()
	if name == "" {
		return nil, errors.New("platform name is empty")
	}
	served := map[string]bool{OpenAPIPath + ".json": true, OpenAPIPath + ".yaml": true, DocsPath: true}
	if !strings.HasPrefix(path, "/") || served[path] || strings.HasPrefix(path, "/reviews/") {
		return nil, fmt.Errorf("platform %s: webhook path %q must start with / and not be one of the server's own", name, path)
	}
	if webhook.WebhookMaxBytes() <= 0 || len(webhook.WebhookContentTypes()) == 0 {
		return nil, fmt.Errorf("platform %s: webhook body limit and content types are required", name)
	}
	s.registerWebhook(api, webhook)
	if s.links != nil {
		s.registerReviewPages(api)
	}
	s.handler = router
	if opts.Recorder != nil {
		s.records = parallel.NewBackground(maxRecordWrites, recordWriteTimeout)
	}
	s.marks = parallel.NewBackground(maxQueuedMarks, queuedMarkTimeout)
	s.fetches = parallel.NewBackground(maxPrefetches, prefetchTimeout)
	s.ctx = ctx
	s.reviews = newReviews(ctx, opts.Workers, opts.Queue, opts.ReviewTimeout, s.review)
	s.reviews.dropped = func(j job) { s.live.forget(pageRefOf(j.pr.Info()), j.live) }
	s.reviews.accepted = s.markQueued
	return s, nil
}

// Handler is the server's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Wait blocks until every review worker has stopped, after the context New
// was given is done, and the records, queued statuses, and prefetches
// started so far are done.
func (s *Server) Wait() {
	s.reviews.wait()
	s.marks.Wait()
	s.fetches.Wait()
	s.records.Wait()
}

// ListenAndServe serves on Options.Addr until ctx is done, then stops
// accepting requests, waits up to Options.ShutdownTimeout for open ones, and waits
// for the review workers (whose reviews ctx cancelled when it is the
// context New was given).
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	s.log.Info("server listening", "component", "server", "addr", s.opts.Addr)

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	s.Wait()
	if err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	return nil
}

// webhookOutput is a webhook's response: 202 once the request verifies.
type webhookOutput struct {
	Body struct {
		Queued bool `json:"queued" doc:"Whether the event started a review. False for events that do not affect reviews, such as a ping or a closed pull request."`
	}
}

// verifiedKey is the context key of a webhook the platform verified.
type verifiedKey struct{}

// verified is a webhook the platform verified: its event type, body, and
// delivery ID.
type verified struct {
	event      string
	body       []byte
	deliveryID string
}

// registerWebhook registers p's webhook route. The platform verifies each
// request in a middleware, before the handler runs, so unauthenticated
// traffic never reaches a platform's parser or the review engine.
func (s *Server) registerWebhook(api huma.API, p PRWebhook) {
	name := p.Name()
	content := map[string]*huma.MediaType{}
	for _, ct := range p.WebhookContentTypes() {
		content[ct] = &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeObject}}
	}
	op := huma.Operation{
		OperationID: "webhook-" + name,
		Method:      http.MethodPost,
		Path:        p.WebhookPath(),
		Summary:     "Receive a " + name + " webhook",
		Description: p.WebhookDescription() + "\n\n" +
			"The platform verifies each request before anything else reads it; one that does not verify is rejected " +
			"with 401. A verified event that starts a review is queued and answered with 202 before the review runs. " +
			"The review reads the rules files at the pull request's base revision, validates the rules files the pull " +
			"request proposes, and reports the outcome as the pull request's status.",
		Tags:          []string{"webhooks"},
		DefaultStatus: http.StatusAccepted,
		MaxBodyBytes:  p.WebhookMaxBytes(),
		RequestBody: &huma.RequestBody{
			Description: "The platform's webhook payload, exactly as the platform sent it.",
			Required:    true,
			Content:     content,
		},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusServiceUnavailable},
		Middlewares: huma.Middlewares{s.verify(api, p)},
	}
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*webhookOutput, error) {
		return s.accept(ctx, p)
	})
}

// verify is the middleware authenticating p's webhooks: it bounds the body,
// has the platform verify the request, and hands the verified event to the
// handler through the context.
func (s *Server) verify(api huma.API, p PRWebhook) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		r, w := humachi.Unwrap(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, p.WebhookMaxBytes())
		event, body, err := p.VerifyWebhook(r)
		if err != nil {
			s.log.Warn("webhook rejected", "component", "server", "platform", p.Name(), "error", err)
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "webhook does not verify")
			return
		}
		// The platform consumed the body; huma reads it again to enforce
		// the documented request body.
		r.Body = io.NopCloser(bytes.NewReader(body))
		id := p.DeliveryID(r)
		if len(id) > maxDeliveryIDLength {
			id = id[:maxDeliveryIDLength]
		}
		next(huma.WithValue(ctx, verifiedKey{}, verified{event: event, body: body, deliveryID: id}))
	}
}

// accept turns a verified webhook into a queued review.
func (s *Server) accept(ctx context.Context, p PRWebhook) (*webhookOutput, error) {
	wh, ok := ctx.Value(verifiedKey{}).(verified)
	if !ok {
		return nil, huma.Error401Unauthorized("webhook not verified")
	}
	log := s.log.With("component", "server", "platform", p.Name(), "event", wh.event, "delivery", wh.deliveryID)
	delivery := DeliveryRecord{Platform: p.Name(), ID: wh.deliveryID, Event: wh.event}
	pr, reviewable, err := p.PullRequest(wh.event, wh.body)
	if err != nil {
		log.Warn("webhook unusable", "error", err)
		s.recordDelivery(delivery, DeliveryUnusable)
		return nil, huma.Error400BadRequest("webhook event is not usable")
	}
	out := &webhookOutput{}
	if !reviewable {
		s.prefetch(p, wh, log)
		s.recordDelivery(delivery, DeliveryIgnored)
		return out, nil
	}
	info := pr.Info()
	delivery.PullRequest = &info
	j := job{platform: p.Name(), pr: pr, deliveryID: wh.deliveryID, queuedAt: time.Now(), order: s.requestOrder(ctx, info, log)}
	j.live = s.live.queue(pageRefOf(info), j.queuedAt)
	if !s.reviews.submit(j) {
		s.live.forget(pageRefOf(info), j.live)
		log.Error("review queue full; webhook dropped")
		s.recordDelivery(delivery, DeliveryQueueFull)
		return nil, huma.Error503ServiceUnavailable("review queue is full")
	}
	s.recordDelivery(delivery, DeliveryQueued)
	out.Body.Queued = true
	log.Info("review queued")
	return out, nil
}

// prefetch starts the work a webhook that starts no review calls for, when
// the platform is a Prefetcher, in the background and under the server's
// lifetime, so stopping the server cancels it.
func (s *Server) prefetch(p PRWebhook, wh verified, log *slog.Logger) {
	pf, ok := p.(Prefetcher)
	if !ok {
		log.Debug("webhook ignored")
		return
	}
	work, ok := pf.Prefetch(wh.event, wh.body)
	if !ok {
		log.Debug("webhook ignored")
		return
	}
	started := s.fetches.Go(func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(s.ctx, cancel)()
		work(ctx)
	})
	if started {
		log.Debug("prefetching for reviews to come")
	} else {
		log.Debug("too many prefetches running; skipped this one")
	}
}

// requestOrder records a review of pr being requested, returning its
// order among every replica's requests, or 0 when it could not be
// recorded (the review then never reports a pass; see report).
func (s *Server) requestOrder(ctx context.Context, pr PullRequestInfo, log *slog.Logger) int64 {
	if s.opts.Recorder == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	order, err := s.opts.Recorder.RequestReview(ctx, pr)
	if err != nil {
		log.Error("recording the review request failed; the review cannot pass", "error", err)
		return 0
	}
	return order
}

// markQueued publishes j's pull request's status as queued in the
// background if j's review has not started after queuedMarkDelay, so the
// pull request shows its review waiting while every worker is busy. The
// scheduler orders it with the key's reviews (reviews.claimMark). It is
// ordered like start across replicas: with a Recorder, in order with every
// replica's reports of the pull request, and not at all when j's order is
// unknown. A queued status that cannot be published (too many are waiting,
// or publishing failed) is skipped; the review runs regardless.
func (s *Server) markQueued(j job) {
	if s.opts.Recorder != nil && j.order == 0 {
		return
	}
	log := s.log.With("component", "server", "platform", j.platform, "key", j.pr.Key())
	link := s.links.reviewURL(j.pr.Info())
	s.marks.Go(func(ctx context.Context) {
		select {
		case <-time.After(queuedMarkDelay):
		case <-ctx.Done():
			return
		}
		release, ok := s.reviews.claimMark(j)
		if !ok {
			return
		}
		defer release()
		queue := func(ctx context.Context) error { return j.pr.Queue(ctx, link) }
		var err error
		if s.opts.Recorder == nil {
			err = queue(ctx)
		} else {
			_, err = s.opts.Recorder.StartInOrder(ctx, j.pr.Info(), j.order, queue)
		}
		if err != nil {
			log.Warn("publishing the review as queued failed", "error", err)
		}
	})
}

// recordDelivery records d, with disposition, in the background.
func (s *Server) recordDelivery(d DeliveryRecord, disposition string) {
	if s.opts.Recorder == nil {
		return
	}
	d.Disposition = disposition
	s.records.Go(func(ctx context.Context) {
		if err := s.opts.Recorder.RecordDelivery(ctx, d); err != nil {
			s.log.Warn("recording a webhook delivery failed", "component", "server", "delivery", d.ID, "error", err)
		}
	})
}

// review runs one queued review and reports its outcome as the pull
// request's status, unless a newer review of its key superseded it
// (current reports false; see reviews), or, with a Recorder, a newer
// review on any replica reported first. While it evaluates, it publishes
// the pull request's status as running (see start), which it waits for
// before it reports, so the running status never replaces the outcome. A
// review that ran out of time still reports, under a fresh deadline; one
// cancelled because the server is stopping does not, and leaves the status
// running. With a Recorder, the review is recorded as it starts and ends.
// Its review page follows it throughout.
func (s *Server) review(ctx context.Context, j job, current func() bool) {
	log := s.log.With("component", "server", "platform", j.platform, "key", j.pr.Key())
	started := time.Now()
	s.live.start(j.live, started)
	link := s.links.reviewURL(j.pr.Info())
	// Recording and publishing the start take round trips of their own, so
	// they run while the review evaluates rather than before it.
	var (
		setup sync.WaitGroup
		id    int64
	)
	setup.Go(func() { id = s.startRecord(ctx, j, log) })
	setup.Go(func() { s.start(ctx, j, link, log) })
	evalCtx := approval.WithProgress(ctx, func(p approval.Progress) { s.live.progress(j.live, p) })
	ev := review.Evaluate(evalCtx, j.pr, s.opts.BlockingLabels, log)
	setup.Wait()
	end := ReviewEnd{
		Outcome:  ev.Outcome,
		Input:    ev.Input,
		State:    ev.State,
		Targets:  targetsOf(ev.State),
		Queued:   started.Sub(j.queuedAt),
		Evaluate: time.Since(started),
	}
	if ev.Err != nil {
		end.Err = ev.Err.Error()
	}
	defer func() {
		s.finishRecord(ctx, id, end, log)
		s.live.finish(pageRefOf(j.pr.Info()), j.live, end, time.Now())
	}()

	if !current() {
		end.Status = ReviewSuperseded
		log.Info("review superseded by a newer one")
		return
	}
	reportCtx := ctx
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var cancel context.CancelFunc
		reportCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), s.opts.ReportTimeout)
		defer cancel()
	} else if ctx.Err() != nil {
		end.Status = ReviewCancelled
		log.Info("review cancelled", "error", ctx.Err())
		return
	}
	// The start waited for a queued status being published, unless the
	// review's context ended first; the outcome must still land after it.
	waitMarked(reportCtx, j)
	reportStart := time.Now()
	s.live.reporting(j.live)
	sent, superseded, err := s.report(reportCtx, j, ev.Outcome, link, log)
	end.Report = time.Since(reportStart)
	end.Outcome = sent
	switch {
	case superseded:
		end.Status = ReviewSuperseded
		log.Info("review superseded by a newer one on another replica")
	case err != nil:
		end.Status = ReviewReportFailed
		end.Err = err.Error()
		log.Error("report failed", "error", err)
	default:
		end.Status = ReviewReported
		log.Info("review reported", "verdict", sent.Verdict, "summary", sent.Summary)
	}
}

// report publishes o as j's status. With a Recorder, it publishes in order
// with every replica's reports of j's pull request
// (Recorder.ReportInOrder), and is superseded, publishing nothing, when a
// newer review already reported.
// When the order cannot be checked, an older review may be publishing over
// a newer one, so it publishes anything but a pass as is, and a pass as
// Errored: it never lets a review that may be stale pass a pull request.
// It returns the outcome it published. The status links to link, j's
// review page, when it is not empty.
func (s *Server) report(ctx context.Context, j job, o approval.Outcome, link string, log *slog.Logger) (approval.Outcome, bool, error) {
	if s.opts.Recorder == nil {
		return o, false, j.pr.Report(ctx, o, link)
	}
	if j.order > 0 {
		called, err := s.opts.Recorder.ReportInOrder(ctx, j.pr.Info(), j.order, func(ctx context.Context) error {
			return j.pr.Report(ctx, o, link)
		})
		switch {
		case called:
			return o, false, err
		case err == nil:
			return o, true, nil
		}
		log.Error("ordering the report failed; the review cannot pass", "error", err)
	}
	if o.Verdict == approval.Passed {
		o = approval.Outcome{Verdict: approval.Errored, Summary: unorderedSummary, Warnings: o.Warnings}
	}
	return o, false, j.pr.Report(ctx, o, link)
}

// start publishes j's pull request's status as running, linking link. With
// a Recorder, it publishes in order with every replica's reports of the
// pull request (Recorder.StartInOrder), and not at all when j's order is
// unknown: a review that may be stale must never replace a newer review's
// report with a running status that nothing would then end. Without one,
// this process's reviews of a key run one at a time, newest last, so none
// starts after a newer one reported. A status that is not published is
// logged; the review runs regardless.
//
// It publishes after any queued status of j's key being published as j
// started (see reviews.claimMark), so that never replaces it.
func (s *Server) start(ctx context.Context, j job, link string, log *slog.Logger) {
	if waitMarked(ctx, j); ctx.Err() != nil {
		return
	}
	publish := func(ctx context.Context) error { return j.pr.Start(ctx, link) }
	var err error
	switch {
	case s.opts.Recorder == nil:
		err = publish(ctx)
	case j.order > 0:
		_, err = s.opts.Recorder.StartInOrder(ctx, j.pr.Info(), j.order, publish)
	default:
		return
	}
	if err != nil {
		log.Warn("publishing the review as running failed", "error", err)
	}
}

// waitMarked waits until the queued status of j's key being published as j
// started is published, or ctx is done.
func waitMarked(ctx context.Context, j job) {
	if j.marked == nil {
		return
	}
	select {
	case <-j.marked:
	case <-ctx.Done():
	}
}

// startRecord records j's review starting, returning its record's ID, or
// 0 when it is not recorded.
func (s *Server) startRecord(ctx context.Context, j job, log *slog.Logger) int64 {
	if s.opts.Recorder == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	id, err := s.opts.Recorder.StartReview(ctx, ReviewStart{
		Platform:    j.platform,
		DeliveryID:  j.deliveryID,
		PullRequest: j.pr.Info(),
		Key:         j.pr.Key(),
		Order:       j.order,
		QueuedAt:    j.queuedAt,
	})
	if err != nil {
		log.Warn("recording the review failed", "error", err)
		return 0
	}
	return id
}

// finishRecord records how review id ended, if it was recorded. It runs
// after the review reported, so it delays no status, and under its own
// deadline, so a review the server cancelled is still recorded.
func (s *Server) finishRecord(ctx context.Context, id int64, end ReviewEnd, log *slog.Logger) {
	if id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := s.opts.Recorder.FinishReview(ctx, id, end); err != nil {
		log.Warn("recording the review's end failed", "review", id, "error", err)
	}
}

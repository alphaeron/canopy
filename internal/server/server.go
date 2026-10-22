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
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/review"
)

// PRWebhook is a hosted platform's pull request webhook: the server
// receives the platform's webhook deliveries through it and gets from each
// the pull request to review. A deployment serves one platform. A new
// platform implements PRWebhook (and PullRequest), and the server registers
// its webhook route without any other change. Everything particular to a
// platform's webhook (where it is served, how big and in what format its
// bodies are, how a request is authenticated, which events matter) is the
// platform's: the server only routes, bounds, verifies through the
// platform, and queues.
type PRWebhook interface {
	// Name identifies the platform in logs and the OpenAPI document.
	Name() string
	// WebhookPath is the path the platform's webhook is served at with
	// POST. It must start with "/" and differ from the server's own paths.
	WebhookPath() string
	// WebhookMaxBytes bounds a webhook request's body: the platform's own
	// payload limit. It must be positive.
	WebhookMaxBytes() int64
	// WebhookContentTypes are the media types the platform's webhook
	// bodies arrive in, as the OpenAPI document lists them. At least one
	// is required.
	WebhookContentTypes() []string
	// WebhookDescription describes the platform's webhook for the OpenAPI
	// document (Markdown): how a request is authenticated, the headers it
	// carries, and which events start a review.
	WebhookDescription() string
	// VerifyWebhook authenticates a webhook request and returns its event
	// type and body. Any error rejects the request; nothing in it is
	// trusted before this returns.
	VerifyWebhook(r *http.Request) (event string, body []byte, err error)
	// PullRequest turns a verified webhook into the pull request to
	// review, or reports false when the event does not start a review. It
	// must not call the platform's API: the pull request does that when
	// reviewed.
	PullRequest(event string, body []byte) (PullRequest, bool, error)
}

// PullRequest is a pull request (or merge request) on a hosted platform,
// as the server reviews it: its files and rules (review.Source, whose
// Revision is the base commit), who wrote and approved it, its files as it
// proposes them, and where its outcome is reported.
type PullRequest interface {
	review.Source

	// ReviewInput reads the pull request's author, its approvers (each
	// reviewer's latest decisive review), which users are active, and the
	// repository owners who review files no rule covers. Names need not be
	// canonical: the review canonicalizes them with the platform's
	// resolver.
	ReviewInput(ctx context.Context) (approval.ReviewInput, error)

	// ReadHead reads a file as the pull request proposes it, so rules files
	// it changes can be validated before they take effect. A path its head
	// does not have wraps fs.ErrNotExist.
	ReadHead(ctx context.Context, path string) ([]byte, error)

	// Report publishes the review's outcome as the pull request's status,
	// replacing the earlier one. The platform maps each verdict to its own
	// status states and carries the summary as the status's description.
	Report(ctx context.Context, o review.Outcome) error
}

// Options configure a Server.
type Options struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// Workers is how many reviews run at once.
	Workers int
	// Queue is how many accepted webhooks may wait for a worker; a webhook
	// arriving when the queue is full is refused with 503.
	Queue int
	// ReviewTimeout bounds each review.
	ReviewTimeout time.Duration
	// Version is the server's version, shown in the OpenAPI document.
	Version string
	// Log receives the server's structured logs; nil means slog.Default().
	Log *slog.Logger
}

// Paths the server serves besides the webhooks.
const (
	// OpenAPIPath is the OpenAPI document, without its extension: huma
	// serves it as .json and .yaml.
	OpenAPIPath = "/openapi"
	// DocsPath is the interactive API documentation.
	DocsPath = "/swagger"
)

// shutdownTimeout bounds how long ListenAndServe waits for open requests
// once its context is done.
const shutdownTimeout = 10 * time.Second

// Server is the review server. Create it with New.
type Server struct {
	opts    Options
	log     *slog.Logger
	handler http.Handler
	pool    *pool
}

// New builds the server over the deployment's one platform webhook and
// starts its review workers. Reviews run under ctx, so cancelling it
// cancels them; Wait waits for the workers to stop.
func New(ctx context.Context, opts Options, webhook PRWebhook) (*Server, error) {
	if webhook == nil {
		return nil, errors.New("no platform to serve")
	}
	if opts.Workers <= 0 || opts.Queue <= 0 || opts.ReviewTimeout <= 0 {
		return nil, fmt.Errorf("workers, queue, and review timeout must be positive, got %d, %d, %s",
			opts.Workers, opts.Queue, opts.ReviewTimeout)
	}
	s := &Server{opts: opts, log: logx.OrDefault(opts.Log)}

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
	if !strings.HasPrefix(path, "/") || served[path] {
		return nil, fmt.Errorf("platform %s: webhook path %q must start with / and not be one of the server's own", name, path)
	}
	if webhook.WebhookMaxBytes() <= 0 || len(webhook.WebhookContentTypes()) == 0 {
		return nil, fmt.Errorf("platform %s: webhook body limit and content types are required", name)
	}
	s.registerWebhook(api, webhook)
	s.handler = router
	s.pool = newPool(ctx, opts.Workers, opts.Queue, opts.ReviewTimeout, s.review)
	return s, nil
}

// Handler is the server's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Wait blocks until every review worker has stopped, after the context New
// was given is done.
func (s *Server) Wait() { s.pool.wait() }

// ListenAndServe serves on Options.Addr until ctx is done, then stops
// accepting requests, waits up to shutdownTimeout for open ones, and waits
// for the review workers (whose reviews ctx cancelled when it is the
// context New was given).
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
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

// verified is a webhook the platform verified: its event type and body.
type verified struct {
	event string
	body  []byte
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
		next(huma.WithValue(ctx, verifiedKey{}, verified{event: event, body: body}))
	}
}

// accept turns a verified webhook into a queued review.
func (s *Server) accept(ctx context.Context, p PRWebhook) (*webhookOutput, error) {
	wh, ok := ctx.Value(verifiedKey{}).(verified)
	if !ok {
		return nil, huma.Error401Unauthorized("webhook not verified")
	}
	log := s.log.With("component", "server", "platform", p.Name(), "event", wh.event)
	pr, reviewable, err := p.PullRequest(wh.event, wh.body)
	if err != nil {
		log.Warn("webhook unusable", "error", err)
		return nil, huma.Error400BadRequest("webhook event is not usable")
	}
	out := &webhookOutput{}
	if !reviewable {
		log.Debug("webhook ignored")
		return out, nil
	}
	if !s.pool.submit(job{platform: p.Name(), pr: pr}) {
		log.Error("review queue full; webhook dropped")
		return nil, huma.Error503ServiceUnavailable("review queue is full")
	}
	out.Body.Queued = true
	log.Info("review queued")
	return out, nil
}

// job is one queued review.
type job struct {
	platform string
	pr       PullRequest
}

// reportTimeout bounds reporting the outcome of a review that ran out of
// time, which can no longer use the review's own context.
const reportTimeout = 30 * time.Second

// review runs one queued review and reports its outcome as the pull
// request's status. A review that ran out of time still reports, under a
// fresh deadline; one cancelled because the server is stopping does not.
func (s *Server) review(ctx context.Context, j job) {
	log := s.log.With("component", "server", "platform", j.platform)
	outcome := evaluate(ctx, j.pr, log)

	reportCtx := ctx
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var cancel context.CancelFunc
		reportCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
		defer cancel()
	} else if ctx.Err() != nil {
		log.Info("review cancelled", "error", ctx.Err())
		return
	}
	if err := j.pr.Report(reportCtx, outcome); err != nil {
		log.Error("report failed", "error", err)
	}
	log.Info("review reported", "verdict", outcome.Verdict, "summary", outcome.Summary)
}

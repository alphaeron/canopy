package server

import (
	"context"
	"net/http"
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
	// DeliveryID is the platform's ID for a webhook delivery, for the
	// server's record; "" when it has none. It is not trusted to be
	// unique: a redelivery may repeat it.
	DeliveryID(r *http.Request) string
	// PullRequest turns a verified webhook into the pull request to
	// review, or reports false when the event does not start a review. It
	// must not call the platform's API: the pull request does that when
	// reviewed.
	PullRequest(event string, body []byte) (PullRequest, bool, error)
}

// Prefetcher is a PRWebhook that prepares for reviews to come from a
// verified webhook that starts none, such as a push to a repository's
// default branch, by reading what those reviews will read into the
// server's caches, so they start warm. The server runs the work in the
// background, a bounded number at once (more are dropped), under the
// server's lifetime, and never waits for it to answer the webhook.
type Prefetcher interface {
	// Prefetch returns the work a verified webhook calls for, or false
	// when it calls for none. Like PullRequest it must not call the
	// platform's API: the work it returns does.
	Prefetch(event string, body []byte) (work func(context.Context), ok bool)
}

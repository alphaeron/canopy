// Package e2e holds the GitHub platform's end-to-end tests: the review
// server runs as a GitHub App against an in-process fake GitHub, which
// delivers signed webhooks as users open pull requests, push, and review,
// and records the check runs the app reports back. It has no non-test code.
package e2e

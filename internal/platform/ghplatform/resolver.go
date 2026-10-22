package ghplatform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
)

// githubName is what GitHub allows in a login, organization, or team slug
// (leniently: it also admits the dots and underscores slugs may carry).
var githubName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// ApproverResolver resolves approver targets against GitHub. An
// "org/team" target is the members of that team (by slug); any other
// target is a user login standing for itself, as under
// DefaultApproverResolver. A malformed target or a team GitHub does not
// have is approval.ErrUnknownTarget; any other API failure is returned as
// is, so the review fails rather than treating the team as approved or
// pruning it. Logins compare case-insensitively (Canonical).
//
// It does not cache: a review resolves each target once (see
// approval.RuleChain.Execute), so one resolver can serve every review
// without membership going stale. It is safe for concurrent use.
type ApproverResolver struct {
	client *github.Client
}

// NewApproverResolver builds a resolver on an authenticated GitHub client.
func NewApproverResolver(client *github.Client) *ApproverResolver {
	return &ApproverResolver{client: client}
}

// Resolve maps target to its members: a team's members, or the login
// itself.
func (r *ApproverResolver) Resolve(ctx context.Context, target string) ([]string, error) {
	if err := r.ValidateTarget(target); err != nil {
		return nil, fmt.Errorf("%w: %w", approval.ErrUnknownTarget, err)
	}
	org, slug, isTeam := strings.Cut(target, "/")
	if !isTeam {
		return []string{target}, nil
	}
	opts := &github.TeamListTeamMembersOptions{ListOptions: github.ListOptions{PerPage: perPage}}
	users, err := paginate(&opts.ListOptions, func() ([]*github.User, *github.Response, error) {
		return r.client.Teams.ListTeamMembersBySlug(ctx, org, slug, opts)
	})
	var ghErr *github.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: team %s: %w", approval.ErrUnknownTarget, target, err)
	}
	if err != nil {
		return nil, fmt.Errorf("list members of team %s: %w", target, err)
	}
	members := make([]string, 0, len(users))
	for _, u := range users {
		members = append(members, u.GetLogin())
	}
	return members, nil
}

// Canonical lowercases a login: GitHub logins are case-insensitive.
func (r *ApproverResolver) Canonical(name string) string { return strings.ToLower(name) }

// ValidateTarget checks that target is a login or an "org/team-slug" in the
// characters GitHub allows. Targets come from rules files, which are
// untrusted input, and go-github interpolates the org and slug into the
// request path unescaped, so nothing else may reach the API.
func (r *ApproverResolver) ValidateTarget(target string) error {
	org, slug, isTeam := strings.Cut(target, "/")
	if !isTeam {
		if !githubName.MatchString(target) {
			return fmt.Errorf("%w: %q: want a GitHub login or <org>/<team-slug>", approval.ErrInvalidTarget, target)
		}
		return nil
	}
	if !githubName.MatchString(org) || !githubName.MatchString(slug) {
		return fmt.Errorf("%w: %q: want <org>/<team-slug>", approval.ErrInvalidTarget, target)
	}
	return nil
}

var (
	_ approval.ApproverResolver = (*ApproverResolver)(nil)
	_ approval.TargetValidator  = (*ApproverResolver)(nil)
)

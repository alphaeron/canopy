package shonin

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/google/go-github/v71/github"
)

// githubName is what GitHub allows in an organization login or team slug.
// Targets come from KIT files, which are untrusted input, and go-github
// interpolates both into the request path unescaped, so anything else is
// rejected before it reaches the API.
var githubName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// RemoteApproverResolver resolves approver targets against GitHub. An
// "org/team" target is the members of that team (by slug); any other
// target is a user login standing for itself, as under
// DefaultApproverResolver. A team that cannot be listed (a malformed name,
// an unknown team, or an API failure) is unresolved, never treated as
// approved.
//
// Team membership is read once per target and cached for the resolver's
// lifetime, since one review resolves the same targets many times. A
// resolver therefore belongs to one review: NewRemote builds one per pull
// request, so membership is never stale across reviews. It is safe for
// concurrent use.
type RemoteApproverResolver struct {
	client *github.Client
	log    *slog.Logger

	mu    sync.Mutex
	teams map[string]resolvedTeam
}

type resolvedTeam struct {
	members []string
	ok      bool
}

// NewRemoteApproverResolver builds a resolver on an authenticated GitHub
// client. log may be nil (defaults to slog.Default()).
func NewRemoteApproverResolver(client *github.Client, log *slog.Logger) *RemoteApproverResolver {
	return &RemoteApproverResolver{client: client, log: log, teams: map[string]resolvedTeam{}}
}

// Resolve maps target to its members: a team's members, or the login
// itself. The EvaluationContext has no bearing on membership.
func (r *RemoteApproverResolver) Resolve(target string, _ EvaluationContext) ([]string, bool) {
	org, slug, isTeam := strings.Cut(target, "/")
	if !isTeam {
		return []string{target}, true
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.teams[target]; ok {
		return t.members, t.ok
	}
	members, err := r.teamMembers(context.Background(), org, slug)
	if err != nil {
		logOrDefault(r.log).Error("resolve: could not list team members",
			"component", "resolver", "phase", "approve", "target", target, "error", err)
	}
	t := resolvedTeam{members: members, ok: err == nil}
	r.teams[target] = t
	return t.members, t.ok
}

// teamMembers lists every member login of org's team slug, following
// pagination.
func (r *RemoteApproverResolver) teamMembers(ctx context.Context, org, slug string) ([]string, error) {
	if !githubName.MatchString(org) || !githubName.MatchString(slug) {
		return nil, fmt.Errorf("invalid team %q: want <org>/<team-slug>", org+"/"+slug)
	}
	opts := &github.TeamListTeamMembersOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var members []string
	for {
		users, resp, err := r.client.Teams.ListTeamMembersBySlug(ctx, org, slug, opts)
		if err != nil {
			return nil, fmt.Errorf("list members of %s/%s: %w", org, slug, err)
		}
		for _, u := range users {
			members = append(members, u.GetLogin())
		}
		if resp.NextPage == 0 {
			return members, nil
		}
		opts.Page = resp.NextPage
	}
}

var _ ApproverResolver = (*RemoteApproverResolver)(nil)

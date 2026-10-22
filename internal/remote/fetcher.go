package remote

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fetcher fetches rules for a remote repository through the GitHub REST API by
// listing the tree at the pull request's base revision and reading each rules
// file as a blob. It is the remote counterpart of local.Fetcher on the same
// Fetcher seam, so the sift and approve phases run the same regardless of
// whether the rules came from a local worktree or a GitHub tree.
type Fetcher struct {
	// Client is the authenticated GitHub client. The caller owns the token
	// wiring (installation token, bot token, or a plain bearer): this type
	// only consumes whatever is wired in, so the fetcher stays
	// transport-agnostic.
	Client *github.Client
	// Owner and name of the remote repository.
	Owner string
	Repo  string
	// BaseSHA is the tree to fetch, normally the pull request's base revision
	// so rules are evaluated against what the reviewers will merge, not what
	// the author is currently pushing.
	BaseSHA string
	// Log receives structured diagnostic output; defaults to slog.Default().
	Log *slog.Logger
}

// Execute runs the fetch phase for a remote repository: it lists the tree at
// BaseSHA, locates every rules file (rulesfile.IsRulesFile, as local.Fetcher
// does), reads each file's contents as a single blob, and parses it into a
// rule list.
// Any per-file read or parse error is accumulated into the returned error;
// the partial result is still provided so callers can report which files
// failed rather than dropping the entire fetch.
func (rf *Fetcher) Execute(ctx context.Context) (string, rulesfile.Set, error) {
	log := rf.log()
	rules := make(rulesfile.Set)

	tree, _, err := rf.Client.Git.GetTree(ctx, rf.Owner, rf.Repo, rf.BaseSHA, true)
	if err != nil {
		log.Error("failed to list base tree",
			"component", "fetcher", "phase", "fetch", "owner", rf.Owner,
			"repo", rf.Repo, "base_sha", rf.BaseSHA, "error", err)
		return "", rules, fmt.Errorf("list base tree %s: %w", rf.BaseSHA, err)
	}

	var fetchErrors []error
	rulesFileCount := 0
	for _, entry := range tree.Entries {
		if entry.GetType() != "blob" {
			continue
		}
		path := entry.GetPath()
		if !rulesfile.IsRulesFile(path) {
			continue
		}
		rulesFileCount++
		data, err := rf.fetchBlobContents(ctx, entry.GetSHA())
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("fetch %s: %w", path, err))
			continue
		}
		fileRules, err := rulesfile.ParseRules(data)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("parse rules file %s: %w", path, err))
			continue
		}
		rules[path] = fileRules
		log.Info("loaded rules from remote rules file",
			"component", "fetcher", "phase", "fetch", "repo", rf.Repo, "path", path)
	}

	if rulesFileCount == 0 {
		log.Error("no rules files found at base revision",
			"component", "fetcher", "phase", "fetch", "repo", rf.Repo, "base_sha", rf.BaseSHA)
	}

	return "", rules, errors.Join(fetchErrors...)
}

// fetchBlobContents reads a single git blob from the repository and returns
// its raw bytes for parsing. The git blob endpoint returns contents
// base64-encoded under the "base64" encoding; the empty-encoding field is the
// raw passthrough some API variants use.
func (rf *Fetcher) fetchBlobContents(ctx context.Context, sha string) ([]byte, error) {
	blob, _, err := rf.Client.Git.GetBlob(ctx, rf.Owner, rf.Repo, sha)
	if err != nil {
		return nil, fmt.Errorf("get blob %s: %w", sha, err)
	}
	switch blob.GetEncoding() {
	case "base64":
		data, err := base64.StdEncoding.DecodeString(blob.GetContent())
		if err != nil {
			return nil, fmt.Errorf("decode blob %s: %w", sha, err)
		}
		return data, nil
	case "":
		return []byte(blob.GetContent()), nil
	default:
		return nil, fmt.Errorf("unsupported blob %s encoding %q", sha, blob.GetEncoding())
	}
}

// log returns the configured logger or the default one.
func (rf *Fetcher) log() *slog.Logger {
	return logOrDefault(rf.Log)
}

var _ approval.Fetcher = (*Fetcher)(nil)

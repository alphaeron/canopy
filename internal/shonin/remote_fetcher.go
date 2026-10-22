package shonin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/kitfile"
)

// RemoteFetcher fetches KIT rules for a remote repository through the GitHub
// REST API by listing the tree at the pull request's base revision and reading
// each KIT file as a blob. It is the remote counterpart of LocalFetcher on the
// same Fetcher seam, so the sift and approve phases run the same regardless of
// whether the rules came from a local worktree or a GitHub tree.
type RemoteFetcher struct {
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
// BaseSHA, locates every KIT file (kitfile.IsKitFile, as LocalFetcher does),
// reads each file's contents as a single blob, and parses it into a rule list.
// Any per-file read or parse error is accumulated into the returned error;
// the partial result is still provided so callers can report which files
// failed rather than dropping the entire fetch.
func (rf *RemoteFetcher) Execute() (string, kitfile.Set, error) {
	log := rf.log()
	rules := make(kitfile.Set)
	ctx := context.Background()

	tree, _, err := rf.Client.Git.GetTree(ctx, rf.Owner, rf.Repo, rf.BaseSHA, true)
	if err != nil {
		log.Error("kit: failed to list base tree",
			"component", "fetcher", "phase", "fetch", "owner", rf.Owner,
			"repo", rf.Repo, "base_sha", rf.BaseSHA, "error", err)
		return "", rules, fmt.Errorf("list base tree %s: %w", rf.BaseSHA, err)
	}

	var fetchErrors []error
	kitFileCount := 0
	for _, entry := range tree.Entries {
		if entry.GetType() != "blob" {
			continue
		}
		path := entry.GetPath()
		if !kitfile.IsKitFile(path) {
			continue
		}
		kitFileCount++
		data, err := rf.fetchBlobContents(ctx, entry.GetSHA())
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("fetch %s: %w", path, err))
			continue
		}
		fileRules, err := kitfile.ParseRules(data)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("parse KIT file %s: %w", path, err))
			continue
		}
		rules[path] = fileRules
		log.Info("kit: loaded rules from remote KIT file",
			"component", "fetcher", "phase", "fetch", "repo", rf.Repo, "path", path)
	}

	if kitFileCount == 0 {
		log.Error("kit: no KIT files found at base revision",
			"component", "fetcher", "phase", "fetch", "repo", rf.Repo, "base_sha", rf.BaseSHA)
	}

	return "", rules, errors.Join(fetchErrors...)
}

// fetchBlobContents reads a single git blob from the repository and returns
// its raw bytes for parsing. The git blob endpoint returns contents
// base64-encoded under the "base64" encoding; the empty-encoding field is the
// raw passthrough some API variants use.
func (rf *RemoteFetcher) fetchBlobContents(ctx context.Context, sha string) ([]byte, error) {
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
func (rf *RemoteFetcher) log() *slog.Logger {
	return logOrDefault(rf.Log)
}

var _ Fetcher = (*RemoteFetcher)(nil)

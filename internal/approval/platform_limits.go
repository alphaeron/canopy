package approval

import "errors"

// ErrTreeTruncated reports that the platform listed a revision's files only
// partially, as a platform may for a very large repository. The listing is
// never used: a missing rules file would silently drop required
// approvers, so a review over a truncated tree fails.
var ErrTreeTruncated = errors.New("repository tree listing is truncated")

// ErrFileListIncomplete reports that the platform listed a change's files
// only partially, as a platform may past its own limit. The partial listing
// is never presented as complete.
var ErrFileListIncomplete = errors.New("changed file listing is incomplete")

// ErrRateLimited reports that the platform refused a request under its rate
// limit for longer than the review could wait.
var ErrRateLimited = errors.New("rate limited by the platform")

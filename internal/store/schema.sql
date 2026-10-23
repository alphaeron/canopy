-- The review server's database schema. Store.ApplySchema applies it at
-- startup; every statement is idempotent. While the project is in
-- development the schema changes in place: a change to an existing table
-- needs the database recreated, as there are no migrations yet.
--
-- Two kinds of data live here, and they are kept apart:
--
--   * Caches of what the platform said (tree_listings, blobs, responses).
--     A cached row is only ever served for the key that produced it, and
--     every key names immutable content (a commit or blob SHA) or is
--     revalidated with the platform before use (an ETag). Nothing that can
--     change on the platform is served from here unchecked. Rows are
--     tamper-evident: a blob is checked against its SHA, and every other
--     row carries an HMAC (mac) under a key the database never sees, so
--     write access to the database alone cannot change the rules a review
--     enforces or the members a team has.
--   * Canopy's own record of what it did (webhook_deliveries, reviews,
--     review_targets, pull_requests). A verdict is never read back from
--     this record: every review evaluates its inputs afresh.
--
-- Store.Prune removes cached rows and idle pull_requests rows past their
-- retention (see prune.go); a pruned cache row costs one more read from
-- the platform. The history (webhook_deliveries, reviews, review_targets)
-- is kept.
--
-- scope is the cache scope (server.RepoCache.Scope), e.g.
-- "github/installation/42": what limits what a reader may see. It leads
-- every cache key, so no installation is served another's rows.
--
-- repo is "owner/name", lower-cased, as server.PullRequest.Key names it.
-- SHAs are lower-case hex: 40 digits for SHA-1 repositories, 64 for
-- SHA-256 ones.

-- tree_listings: the blobs of a repository's tree at one commit, as
-- server.TreeBlob by path. A commit's tree never changes, so a row never
-- goes stale. listing is encoded by the store package; only whole
-- listings are kept, never a truncated or partial one.
CREATE TABLE IF NOT EXISTS tree_listings (
    scope      text        NOT NULL,
    repo       text        NOT NULL,
    commit_sha text        NOT NULL CHECK (commit_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    blob_count integer     NOT NULL CHECK (blob_count >= 0),
    listing    bytea       NOT NULL,
    mac        bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, repo, commit_sha)
);
CREATE INDEX IF NOT EXISTS tree_listings_created_idx ON tree_listings (created_at);

-- blobs: file contents by git blob SHA, a hash of the contents, so a row
-- never goes stale. Only rules files are kept, so contents are bounded by
-- rulesfile.MaxSize (512 KiB). The reader recomputes the blob SHA from the
-- contents and treats a mismatch as a miss.
CREATE TABLE IF NOT EXISTS blobs (
    scope      text        NOT NULL,
    sha        text        NOT NULL CHECK (sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    content    bytea       NOT NULL CHECK (octet_length(content) <= 524288),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, sha)
);
CREATE INDEX IF NOT EXISTS blobs_created_idx ON blobs (created_at);

-- responses: the last response to a platform GET whose answer can change
-- (a team's members, a user, a pull request's reviews), with its ETag. The
-- body is served only after the platform answers a conditional request for
-- the same scope and URL with 304 Not Modified, so it is never used
-- without the platform confirming it is current. url is the full request
-- URL, query included (each page of a listing is its own row). header is
-- the JSON of the response headers kept with the body.
CREATE TABLE IF NOT EXISTS responses (
    scope      text        NOT NULL,
    url        text        NOT NULL,
    etag       text        NOT NULL,
    header     bytea       NOT NULL,
    body       bytea       NOT NULL,
    mac        bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, url)
);
CREATE INDEX IF NOT EXISTS responses_updated_idx ON responses (updated_at);

-- webhook_deliveries: every verified webhook. Unverified requests are not
-- recorded, so unauthenticated traffic cannot grow the table. delivery_id
-- is the platform's delivery ID (GitHub: X-GitHub-Delivery); it is not
-- unique, as a redelivery repeats it. No payload is kept. The pull request
-- columns are null for a delivery that names none the server could read.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    platform    text        NOT NULL,
    delivery_id text        NOT NULL,
    event       text        NOT NULL,
    action      text,
    scope       text,
    repo        text,
    pr_number   integer     CHECK (pr_number > 0),
    head_sha    text,
    disposition text        NOT NULL CHECK (disposition IN ('queued', 'ignored', 'unusable', 'queue_full')),
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_deliveries_delivery_idx ON webhook_deliveries (platform, delivery_id);
CREATE INDEX IF NOT EXISTS webhook_deliveries_pr_idx ON webhook_deliveries (repo, pr_number, received_at DESC);

-- pull_requests: one row per pull request, ordering the reviews requested
-- for it across every replica of the server. Accepting a webhook
-- increments requested_order; a review reports only if its order is
-- greater than reported_order, under a lock on the pull request held until
-- its report is published and reported_order updated. So reports are
-- published in the order their reviews were requested, and an older review
-- that saw a since-dismissed approval can never report after a newer one,
-- on any replica. Reports are ordered per pull request, not per head
-- commit: a review superseded by one of a newer head only skips reporting
-- on a commit that is no longer the head. A row idle past its retention is
-- pruned; its next request starts again at 1, which is safe because no
-- review outlives the retention (see prune.go).
CREATE TABLE IF NOT EXISTS pull_requests (
    scope           text        NOT NULL,
    repo            text        NOT NULL,
    pr_number       integer     NOT NULL CHECK (pr_number > 0),
    requested_order bigint      NOT NULL CHECK (requested_order > 0),
    reported_order  bigint      NOT NULL DEFAULT 0 CHECK (reported_order >= 0),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, repo, pr_number),
    CHECK (reported_order <= requested_order)
);
CREATE INDEX IF NOT EXISTS pull_requests_updated_idx ON pull_requests (updated_at);

-- reviews: one row per review run, for history and visualization.
--
--   delivery_id   the delivery that requested the review (see
--                 webhook_deliveries; not unique there).
--   review_key    server.PullRequest.Key: where the review reports.
--   request_order the review's order among its pull request's requests
--                 (pull_requests); 0 when it could not be recorded.
--   status        running until the review ends; then reported,
--                 superseded (a newer review of its key replaced it),
--                 cancelled (the server stopped), or report_failed.
--   verdict       approval.Verdict published (or reached, if none was);
--                 null while running.
--   input         the review input as used: author, approvers, owners.
--   state         approval.ReviewState as JSON (ReviewState.MarshalJSON).
--   error         why the review could not run or report, for operators:
--                 it may quote platform errors, never tokens.
CREATE TABLE IF NOT EXISTS reviews (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    delivery_id   text        NOT NULL,
    platform      text        NOT NULL,
    scope         text        NOT NULL,
    repo          text        NOT NULL,
    pr_number     integer     NOT NULL CHECK (pr_number > 0),
    base_sha      text        NOT NULL,
    head_sha      text        NOT NULL,
    author        text        NOT NULL,
    action        text        NOT NULL,
    review_key    text        NOT NULL,
    request_order bigint      NOT NULL CHECK (request_order >= 0),
    status        text        NOT NULL DEFAULT 'running'
                              CHECK (status IN ('running', 'reported', 'superseded', 'cancelled', 'report_failed')),
    verdict       text        CHECK (verdict IN ('passed', 'pending', 'failed', 'errored')),
    summary       text,
    items         text[],
    warnings      text[],
    input         jsonb,
    state         jsonb,
    error         text,
    queued_ms     integer     CHECK (queued_ms >= 0),
    evaluate_ms   integer     CHECK (evaluate_ms >= 0),
    report_ms     integer     CHECK (report_ms >= 0),
    queued_at     timestamptz NOT NULL,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    CHECK ((status = 'running') = (finished_at IS NULL)),
    CHECK (status = 'running' OR verdict IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS reviews_pr_idx ON reviews (repo, pr_number, started_at DESC);
CREATE INDEX IF NOT EXISTS reviews_started_idx ON reviews (started_at DESC);

-- review_targets: each approver target a review evaluated. It answers
-- which pull requests a change in a team's membership affects (their
-- latest review depended on the team); it is never read to resolve a
-- target. resolved is false when the target could not be resolved.
CREATE TABLE IF NOT EXISTS review_targets (
    review_id bigint  NOT NULL REFERENCES reviews (id) ON DELETE CASCADE,
    target    text    NOT NULL,
    resolved  boolean NOT NULL,
    PRIMARY KEY (review_id, target)
);
CREATE INDEX IF NOT EXISTS review_targets_target_idx ON review_targets (target);

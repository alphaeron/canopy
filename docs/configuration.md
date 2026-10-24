# Configuration

The CLI and the review server read the same configuration, in decreasing precedence: environment variables prefixed `CANOPY_`, the file given with `--config`, the file named by `CANOPY_CONFIG`, then `.canopy.yaml` in the current directory or `$HOME`. Each hosted platform has its own section.

| Key | Environment variable | Default | Purpose |
|---|---|---|---|
| `verbose` | `CANOPY_VERBOSE` | `false` | Debug-level logging |
| `serve_addr` | `CANOPY_SERVE_ADDR` | `:8080` | Review server listen address |
| `review_timeout` | `CANOPY_REVIEW_TIMEOUT` | `50s` | Longest one review may run |
| `report_timeout` | `CANOPY_REPORT_TIMEOUT` | `30s` | Longest reporting the outcome of a timed-out review may take |
| `review_workers` | `CANOPY_REVIEW_WORKERS` | `16` | Reviews the server runs at once |
| `review_queue_per_worker` | `CANOPY_REVIEW_QUEUE_PER_WORKER` | `16` | Pull requests that may wait per worker; beyond that the server answers 503. More webhooks for a pull request already waiting or under review take no more room. `review_workers` × `review_queue_per_worker` may be at most 4096 |
| `shutdown_timeout` | `CANOPY_SHUTDOWN_TIMEOUT` | `10s` | Longest the server waits for open requests when stopping |
| `database_url` | `CANOPY_DATABASE_URL` | | Review server's PostgreSQL connection string; unset runs without a database (see [Database](server.md#database)). The server creates its tables at startup. Never logged |
| `public_url` | `CANOPY_PUBLIC_URL` | | Review server's URL as users reach it, e.g. `https://canopy.example.com`; with `link_key`, serves review pages and links each check run to its own (see [Review pages](server.md#review-pages)) |
| `link_key` | `CANOPY_LINK_KEY` | | Required with `public_url`: at least 32 bytes (e.g. `openssl rand -hex 32`), the same on every replica. Signs review page links; changing it breaks every link already published. Never logged |
| `blocking_labels` | `CANOPY_BLOCKING_LABELS` | | Labels that fail a change carrying any of them, whatever its approvals, e.g. `["do not merge"]` (see [Blocking Labels](review.md#blocking-labels)). The environment variable is comma-separated |
| `link_lifetime` | `CANOPY_LINK_LIFETIME` | `2160h` (90 days) | How long a review page link works after the server signs it; at least `1h`. A Go duration, which has no day unit: `168h` is 7 days. A change applies to links signed from then on; to cut off links already published, change `link_key` |
| `database_key` | `CANOPY_DATABASE_KEY` | | Required with `database_url`: at least 32 bytes (e.g. `openssl rand -hex 32`), the same on every replica, never stored in the database. Authenticates the GitHub data cached there; changing it discards that cache. Never logged |
| `github.app_id` | `CANOPY_GITHUB_APP_ID` | | GitHub App ID |
| `github.private_key` | `CANOPY_GITHUB_PRIVATE_KEY` | | GitHub App private key (PEM, PKCS #1 or #8) |
| `github.webhook_secret` | `CANOPY_GITHUB_WEBHOOK_SECRET` | | GitHub webhook signing secret |
| `github.api_url` | `CANOPY_GITHUB_API_URL` | `https://api.github.com/` | REST API base URL, for GitHub Enterprise Server; must be `https` (`http` only on a loopback host) |
| `github.request_timeout` | `CANOPY_GITHUB_REQUEST_TIMEOUT` | `30s` | Longest one REST API request may wait for a response |

```yaml
verbose: false
blocking_labels:
  - do not merge
github:
  app_id: "12345"
  private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    ...
  webhook_secret: "..."
```

The platform is deployment configuration: a repository under review has no say in it. The review server reviews on the platform whose section has any key set. That section must then be complete and valid, so setting only some of a platform's keys stops the server at startup, as does configuring more than one platform.

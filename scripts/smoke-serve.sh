#!/bin/sh
# smoke-serve.sh — end-to-end smoke test for the canopyweb review server.
# Invoked by `make smoke-serve` with the pre-built binary path as its only
# argument. It starts the server on a free local port with a throw-away
# GitHub App configuration, checks the OpenAPI document, the docs page, and
# that an unsigned webhook is rejected, then stops the server. It first
# checks that a partly configured platform keeps the server from starting.
# No request reaches GitHub.
set -eu

BIN="${1:?usage: smoke-serve.sh <path-to-canopyweb-binary>}"
PORT="${SMOKE_PORT:-18080}"
URL="http://127.0.0.1:$PORT"

work=$(mktemp -d)
pid=""
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT

fail() { echo "smoke-serve: FAIL: $1" >&2; [ -f "$work/log" ] && cat "$work/log" >&2; exit 1; }

openssl genrsa -out "$work/key.pem" 2048 2>/dev/null || fail "generate key"
# An empty config file keeps the user's own ~/.canopy.yaml out of the test.
printf 'verbose: false\n' >"$work/config.yaml"

# A partly configured platform must stop the server before it listens.
if CANOPY_CONFIG="$work/config.yaml" CANOPY_GITHUB_APP_ID=1 "$BIN" --addr "127.0.0.1:$PORT" >"$work/log" 2>&1; then
	fail "server started with only github.app_id set"
fi
grep -q 'partly configured' "$work/log" || fail "partial configuration error not reported"

CANOPY_CONFIG="$work/config.yaml" \
CANOPY_GITHUB_APP_ID=1 \
CANOPY_GITHUB_PRIVATE_KEY="$(cat "$work/key.pem")" \
CANOPY_GITHUB_WEBHOOK_SECRET=smoke \
	"$BIN" --addr "127.0.0.1:$PORT" >"$work/log" 2>&1 &
pid=$!

i=0
until curl -fs "$URL/openapi.json" >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -gt 50 ] && fail "server did not start"
	kill -0 "$pid" 2>/dev/null || fail "server exited"
	sleep 0.1
done

curl -fs "$URL/openapi.json" | grep -q '"/webhooks/github"' || fail "openapi.json lists the webhook"
curl -fs "$URL/swagger" | grep -q 'swagger-ui' || fail "swagger docs page"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
	-H 'X-GitHub-Event: pull_request' -d '{}' "$URL/webhooks/github")
[ "$code" = 401 ] || fail "unsigned webhook answered $code, want 401"

echo "smoke-serve: ok"

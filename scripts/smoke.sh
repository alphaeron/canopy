#!/bin/sh
# smoke.sh — end-to-end smoke test for the kit CLI binary. Invoked by
# `make smoke` with the pre-built binary path as its only argument. It
# drives the entire scripted command surface against a throw-away git
# repository, asserting the output of each command in turn. Exits nonzero
# on the first failure, so it gates CI the same way `go test` does.
set -eu

BIN="${1:?usage: smoke.sh <path-to-kit-binary>}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

git init -q .
git config user.email smoke@kit.test
git config user.name smoke

mkdir -p src/api

fail() { echo "smoke: FAIL: $1" >&2; exit 1; }

# --- 1. no-commit repo degrades to empty universes, no error -------------
printf 'package main\n' > main.go
printf 'package api\n' > src/api/h.go
$BIN validate --all >/dev/null   # must not error even before the first commit

# --- 2. init scaffolds a KIT file and a rule ------------------------------
$BIN init --pattern '.*\.go' --name 'go review' --approver "team/go" --approver "1" --json \
    | grep -q '"RequiredCount": 1' || fail "init --json"
grep -q "name: go review" KIT || fail "init KIT content"

git add -A && git commit -qm init

# --- 3. validate ----------------------------------------------------------
$BIN validate --all | grep -q "are valid" || fail "validate --all"

# --- 4. rules list / get --------------------------------------------------
$BIN rules list --json | grep -q '"Pattern": ".*\\\\\.go"' || fail "rules list --json"
$BIN rules get ".*\.go" --json | grep -q 'main.go' || fail "rules get --json"

# --- 5. rules add / edit --------------------------------------------------
$BIN rules add --pattern ".*\.md" --name "docs" --strategy intersect \
    --approver "team/docs" --approver "1" | grep -q "added rule" || fail "rules add"
$BIN rules edit ".*\.md" --name "docs-v2" --self-approve | grep -q "edited rule" || fail "rules edit"
$BIN rules list --json | grep -q '"Name": "docs-v2"' || fail "rules edit persisted"

# --- 6. rules files / match / coverage ------------------------------------
$BIN rules files "KIT" | grep -q "^KIT$" || fail "rules files"
$BIN rules files "main\.go" | grep -q "main.go" && fail "rules files lists only KIT files"
$BIN match --json | grep -q '"path": "main.go"' || fail "match --json"
$BIN match --by-scope --scope "go review" --json | grep -q '"files"' || fail "match --by-scope"
$BIN coverage --json | grep -q '"universe"' || fail "coverage --json"
$BIN review 'main\.go' --approver "team/go" --json | grep -q '"overall": "satisfied"' || fail "review --json"
$BIN review 'main\.go' | grep -q "^Overall: missing approvers" || fail "review table"

# --- 7. rules files lists every KIT file without a pattern ----------------
$BIN rules files --json | grep -q '"KIT"' || fail "rules files --json"

# --- 8. validate flags a malformed rule and exits nonzero -----------------
# The malformed file must be tracked before validate --all can see it: the
# command walks the committed tree, not the working directory.
mkdir -p src/kit-bad
printf '.*\\(unclosed:\n  name: bad\n' > src/kit-bad/KIT
git add -A && git commit -qm "add malformed rule"
if $BIN validate --all >/dev/null 2>&1; then fail "validate should fail on malformed rule"; fi

# --- 9. rules remove: declined without confirm, applied with --force ------
printf 'n\n' | $BIN rules remove ".*\.md" 2>&1 | grep -q "remove aborted" || fail "remove aborted"
$BIN rules list --json | grep -q '"Name": "docs-v2"' || fail "declined remove left rule intact"
$BIN rules remove ".*\.md" --force | grep -q "removed rule" || fail "remove --force"
$BIN rules list --json | grep -q '"Pattern": ".*\\\\\.go"' || fail "original rule retained"

echo "smoke: ok"

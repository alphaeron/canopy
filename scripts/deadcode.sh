#!/bin/sh
# deadcode.sh — fails when any function is unreachable from the module's
# commands (cmd/kit and cmd/kitweb), apart from those scripts/deadcode.allow
# lists. Invoked by `make deadcode`; the
# deadcode tool runs at a pinned version without being added to go.mod.
set -eu

DEADCODE=golang.org/x/tools/cmd/deadcode@v0.38.0
allow="$(dirname "$0")/deadcode.allow"

funcs=$(go run "$DEADCODE" -f '{{range .Funcs}}{{$.Path}}.{{.Name}}{{"\n"}}{{end}}' ./...)
patterns=$(grep -v -e '^#' -e '^$' "$allow")
dead=$(printf '%s\n' "$funcs" | grep -v -E -e "$patterns" | grep -v '^$' || true)

if [ -n "$dead" ]; then
	echo "deadcode: unreachable from the commands (remove it, or list it in $allow):"
	printf '  %s\n' $dead
	exit 1
fi
echo "deadcode: ok"

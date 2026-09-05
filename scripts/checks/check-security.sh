#!/bin/sh
set -u

cd "$(dirname "$0")/../.."
export CI=1

step() {
	printf '\n==> %s\n' "$1"
}

if [ ! -f web/frontend/dist/index.html ]; then
	printf 'ERROR: embedded frontend assets are missing; run make bootstrap first.\n' >&2
	exit 1
fi

status=0

step "Running pinned Go vulnerability analysis (govulncheck v1.7.0)"
if ! go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...; then
	status=1
fi

step "Auditing locked frontend dependencies"
if ! npm --prefix web/frontend audit --audit-level=high; then
	status=1
fi

if [ "$status" -ne 0 ]; then
	printf '\nERROR: one or more security checks reported findings or could not run.\n' >&2
	exit 1
fi

printf '\nSecurity checks passed.\n'

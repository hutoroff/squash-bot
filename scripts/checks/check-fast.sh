#!/bin/sh
set -eu

cd "$(dirname "$0")/../.."
export CI=1

step() {
	printf '\n==> %s\n' "$1"
}

asset_marker=web/frontend/dist/index.html
if [ ! -f "$asset_marker" ]; then
	printf 'ERROR: embedded frontend assets are missing; run make bootstrap first.\n' >&2
	exit 1
fi
if find web/frontend/src web/frontend/index.html web/frontend/package.json \
	web/frontend/package-lock.json web/frontend/tsconfig.json \
	web/frontend/tsconfig.node.json web/frontend/vite.config.ts \
	-type f -newer "$asset_marker" -print -quit | grep -q .; then
	printf 'ERROR: embedded frontend assets may be stale; run make check to rebuild and verify them.\n' >&2
	exit 1
fi

step "Checking Go formatting"
unformatted=$(find . -type f -name '*.go' -not -path './.git/*' -print | while IFS= read -r file; do gofmt -l "$file"; done)
if [ -n "$unformatted" ]; then
	printf 'ERROR: gofmt is required for:\n%s\n' "$unformatted" >&2
	exit 1
fi

step "Checking diff whitespace"
git diff --check
git diff --cached --check

step "Running go vet"
go vet ./...

step "Type-checking the frontend application"
npm --prefix web/frontend run typecheck

step "Running Go unit tests"
go test -count=1 -timeout 120s ./...

step "Running frontend tests"
npm --prefix web/frontend test

printf '\nFast checks passed.\n'

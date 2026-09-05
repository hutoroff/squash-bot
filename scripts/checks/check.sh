#!/bin/sh
set -eu

cd "$(dirname "$0")/../.."
export CI=1

step() {
	printf '\n==> %s\n' "$1"
}

step "Rebuilding embedded frontend assets"
npm --prefix web/frontend run build

step "Building all Go packages"
go build ./...

./scripts/checks/check-fast.sh

step "Running race-enabled Go tests"
go test -race -count=1 -timeout 120s ./...

step "Running PostgreSQL integration tests"
go test -count=1 -tags integration -timeout 120s ./...

step "Running the service/database lifecycle test"
go test -count=1 -tags e2e -timeout 120s ./tests/e2e/...

printf '\nFull checks passed.\n'

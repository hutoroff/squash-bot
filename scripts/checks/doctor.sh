#!/bin/sh
set -u

cd "$(dirname "$0")/../.."

failed=0

ok() {
	printf 'ok: %s\n' "$1"
}

problem() {
	printf 'ERROR: %s\n' "$1" >&2
	failed=1
}

version_at_least() {
	awk -v actual="$1" -v required="$2" 'BEGIN {
		split(actual, a, ".")
		split(required, r, ".")
		for (i = 1; i <= 3; i++) {
			a[i] += 0
			r[i] += 0
			if (a[i] > r[i]) exit 0
			if (a[i] < r[i]) exit 1
		}
		exit 0
	}'
}

printf 'Checking local development prerequisites (no .env files are loaded).\n'

required_go=$(awk '$1 == "go" { print $2; exit }' go.mod)
if ! command -v go >/dev/null 2>&1; then
	problem "Go is not installed (required: ${required_go})"
else
	actual_go=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
	if [ -z "$actual_go" ]; then
		problem "could not determine the installed Go version"
	elif version_at_least "$actual_go" "$required_go"; then
		ok "Go ${actual_go} (minimum ${required_go})"
	else
		problem "Go ${actual_go} is older than required ${required_go}"
	fi
fi

required_node=$(tr -d '[:space:]' < web/frontend/.node-version)
if ! command -v node >/dev/null 2>&1; then
	problem "Node is not installed (required version: ${required_node})"
else
	actual_node=$(node --version 2>/dev/null | sed 's/^v//')
	case "$actual_node" in
		"$required_node"|"$required_node".*) ok "Node ${actual_node} (configured ${required_node})" ;;
		*) problem "Node ${actual_node} does not match web/frontend/.node-version (${required_node})" ;;
	esac
fi

if ! command -v npm >/dev/null 2>&1; then
	problem "npm is not installed"
else
	npm_version=$(npm --version 2>/dev/null || true)
	if [ -n "$npm_version" ]; then
		ok "npm ${npm_version}"
	else
		problem "npm is installed but did not report a version"
	fi
fi

if ! command -v docker >/dev/null 2>&1; then
	problem "Docker CLI is not installed (required by make check)"
elif docker info >/dev/null 2>&1; then
	docker_version=$(docker version --format '{{.Server.Version}}' 2>/dev/null || true)
	ok "Docker daemon is reachable${docker_version:+ (server ${docker_version})}"
else
	problem "Docker CLI is installed but the daemon is not reachable (required by make check)"
fi

asset_marker=web/frontend/dist/index.html
if [ ! -f "$asset_marker" ]; then
	problem "embedded frontend assets are missing; run make bootstrap"
elif find web/frontend/src web/frontend/index.html web/frontend/package.json \
	web/frontend/package-lock.json web/frontend/tsconfig.json \
	web/frontend/tsconfig.node.json web/frontend/vite.config.ts \
	-type f -newer "$asset_marker" -print -quit | grep -q .; then
	problem "embedded frontend assets may be stale; run make check (or make bootstrap after dependency changes)"
else
	ok "embedded frontend assets are present and newer than frontend inputs"
fi

if [ "$failed" -ne 0 ]; then
	printf '\nDoctor found missing or unusable prerequisites. Fix the errors above and rerun make doctor.\n' >&2
	exit 1
fi

printf '\nAll prerequisites are ready.\n'

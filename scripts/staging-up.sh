#!/bin/sh
# Opt-in deployment on a dedicated staging host with a clean checkout.
# Do not use this on the production Docker host or with the production bot/env.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"

fail() { printf 'staging-up: %s\n' "$1" >&2; exit 1; }
[ "$#" -eq 0 ] || fail 'no arguments; supply STAGING_ENV_FILE and STAGING_DEDICATED_HOST=yes'
[ "${STAGING_DEDICATED_HOST:-}" = yes ] || fail 'requires STAGING_DEDICATED_HOST=yes (dedicated host, not production Docker)'
[ "$(uname -s)" = Linux ] || fail 'requires a dedicated Linux host; do not start services on this Mac'
[ -z "${DOCKER_HOST:-}" ] && [ -z "${DOCKER_CONTEXT:-}" ] && [ -z "${DOCKER_CONFIG:-}" ] || fail 'remote/custom Docker targets are not supported; run on the dedicated host'
command -v docker >/dev/null 2>&1 || fail 'Docker CLI is required'
command -v node >/dev/null 2>&1 || fail 'Node is required for staging file validation and the web image build'
command -v git >/dev/null 2>&1 || fail 'Git is required to tag the exact candidate'
# `docker context show` is not available in some otherwise supported Docker CLIs.
contexts=$(env -i PATH="$PATH" HOME="${HOME:-/tmp}" docker context ls --format '{{if .Current}}{{.Name}}{{end}}') || fail 'cannot inspect the active Docker context'
active_context=$(printf '%s\n' "$contexts" | tr -d '\n')
[ "$active_context" = default ] || fail 'use the local default Docker context on the dedicated staging host'
compose_version=$(env -i PATH="$PATH" HOME="${HOME:-/tmp}" docker compose version --short) || fail 'Docker Compose v2.36+ is required'
node -e 'const m=/^v?(\d+)\.(\d+)\./.exec(process.argv[1]); if(!m || +m[1]<2 || (+m[1]===2 && +m[2]<36)) process.exit(1)' "$compose_version" || fail 'Docker Compose v2.36+ is required for isolated interface binding'

[ -n "${STAGING_ENV_FILE:-}" ] || fail 'set STAGING_ENV_FILE to an absolute, private file outside the checkout'
case "$STAGING_ENV_FILE" in /*) ;; *) fail 'STAGING_ENV_FILE must be absolute' ;; esac
[ -f "$STAGING_ENV_FILE" ] && [ ! -L "$STAGING_ENV_FILE" ] || fail 'STAGING_ENV_FILE must be an existing regular file, not a symlink'
resolved_env=$(node -e 'const fs=require("fs"); const p=fs.realpathSync(process.argv[1]); if(fs.statSync(p).mode & 0o077) process.exit(1); process.stdout.write(p)' "$STAGING_ENV_FILE") || fail 'STAGING_ENV_FILE must be owner-only (mode 0600 or 0400)'
case "$resolved_env" in "$root"|"$root"/*) fail 'STAGING_ENV_FILE must not reside in the checkout' ;; esac
export STAGING_ENV_FILE="$resolved_env"

[ -z "$(git status --porcelain --untracked-files=normal)" ] || fail 'commit or remove local changes before building a revision-tagged image'
STAGING_REV=$(git rev-parse --short=12 HEAD)
export STAGING_REV

# Validate without printing the rendered environment (which includes secrets).
# Do not let an exported production secret override a staging --env-file value.
compose() { env -i PATH="$PATH" HOME="${HOME:-/tmp}" STAGING_ENV_FILE="$STAGING_ENV_FILE" STAGING_REV="$STAGING_REV" docker compose --project-name squash-staging --env-file "$STAGING_ENV_FILE" -f "$root/docker-compose.staging.yml" "$@"; }
compose config --quiet || fail 'staging configuration failed validation; nothing was started'
printf 'staging-up: building committed revision %s on the dedicated staging host\n' "$STAGING_REV"
compose up --build --detach --wait || fail 'startup/readiness failed; inspect the staging project; no production rollback or cleanup was attempted'
printf 'staging-up: staging containers are healthy/running; inspect scrapes and business gates separately\n'

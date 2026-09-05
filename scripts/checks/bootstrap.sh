#!/bin/sh
# Internal recipe; use make bootstrap for its deadline/controller.
set -eu

cd "$(dirname "$0")/../.."
export CI=1

printf '\n==> Installing locked frontend dependencies\n'
npm --prefix web/frontend ci
printf '\n==> Building embedded frontend assets\n'
npm --prefix web/frontend run build

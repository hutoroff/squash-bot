#!/bin/sh
set -eu

cd "$(dirname "$0")/../.."
export CI=1
exec node scripts/checks/security.mjs

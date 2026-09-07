#!/usr/bin/env bash
# Lightweight health monitor for production.
# Checks service health/readiness endpoints and sends a Telegram alert on failure.
#
# Install in cron:
#   */5 * * * * /opt/squash-bot/scripts/healthcheck.sh
#
# Requires two environment variables (or edit the defaults below):
#   HEALTHCHECK_BOT_TOKEN  — Telegram bot token (same as the bot uses)
#   HEALTHCHECK_CHAT_ID    — Telegram chat ID to send alerts to (your personal chat)
set -euo pipefail

BOT_TOKEN="${HEALTHCHECK_BOT_TOKEN:-}"
CHAT_ID="${HEALTHCHECK_CHAT_ID:-}"

COMPOSE_FILE="docker-compose.prod.yml"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

if [ -z "$BOT_TOKEN" ] || [ -z "$CHAT_ID" ]; then
  echo "ERROR: Set HEALTHCHECK_BOT_TOKEN and HEALTHCHECK_CHAT_ID" >&2
  exit 1
fi

send_alert() {
  local msg="$1"
  curl -s -X POST "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" \
    -d "chat_id=${CHAT_ID}" \
    -d "text=${msg}" \
    -d "parse_mode=HTML" > /dev/null 2>&1
}

FAILED=""

# Management retains its public liveness probe.
MANAGEMENT_CONTAINER=$(docker compose -f "$PROJECT_DIR/$COMPOSE_FILE" ps -q management 2>/dev/null || true)
if [ -z "$MANAGEMENT_CONTAINER" ]; then
  FAILED="${FAILED}management (container not found)\n"
elif ! docker exec "$MANAGEMENT_CONTAINER" wget -qO- "http://localhost:8080/health" > /dev/null 2>&1; then
  FAILED="${FAILED}management (/health failed)\n"
fi

# Booking's richer readiness endpoint is internal and monitoring-authenticated.
# Expand the token only inside the container so its value is not placed in the
# host docker command line or this script's diagnostics.
BOOKING_CONTAINER=$(docker compose -f "$PROJECT_DIR/$COMPOSE_FILE" ps -q booking 2>/dev/null || true)
if [ -z "$BOOKING_CONTAINER" ]; then
  FAILED="${FAILED}booking (container not found)\n"
elif ! docker exec "$BOOKING_CONTAINER" sh -c \
  'test -n "$BOOKING_MONITORING_SECRET" && wget -qO- --header="Authorization: Bearer $BOOKING_MONITORING_SECRET" "http://localhost:${BOOKING_ADMIN_PORT:-9091}/health/ready"' \
  > /dev/null 2>&1; then
  FAILED="${FAILED}booking (/health/ready failed)\n"
fi

# Check telegram container is running (no /health endpoint).
TG_CONTAINER=$(docker compose -f "$PROJECT_DIR/$COMPOSE_FILE" ps -q telegram 2>/dev/null || true)
if [ -z "$TG_CONTAINER" ]; then
  FAILED="${FAILED}telegram (container not found)\n"
elif [ "$(docker inspect -f '{{.State.Running}}' "$TG_CONTAINER" 2>/dev/null)" != "true" ]; then
  FAILED="${FAILED}telegram (not running)\n"
fi

if [ -n "$FAILED" ]; then
  send_alert "$(printf '<b>Squash Bot Health Alert</b>\n\n%b' "$FAILED")"
fi

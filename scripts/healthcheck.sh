#!/usr/bin/env bash
# Lightweight health monitor for production.
# Checks /health endpoints and sends a Telegram alert on failure.
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

# Check services that expose /health endpoints.
SERVICES=("management:8080" "booking:8081")

for entry in "${SERVICES[@]}"; do
  NAME="${entry%%:*}"
  PORT="${entry##*:}"

  CONTAINER=$(docker compose -f "$PROJECT_DIR/$COMPOSE_FILE" ps -q "$NAME" 2>/dev/null || true)
  if [ -z "$CONTAINER" ]; then
    FAILED="${FAILED}${NAME} (container not found)\n"
    continue
  fi

  if ! docker exec "$CONTAINER" wget -qO- "http://localhost:${PORT}/health" > /dev/null 2>&1; then
    FAILED="${FAILED}${NAME} (/health failed)\n"
  fi
done

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

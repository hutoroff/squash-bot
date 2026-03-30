#!/usr/bin/env bash
# Deploy / update all services on the production server.
# Run from the project directory (e.g. /opt/squash-bot):
#   ./scripts/deploy.sh          — pull all images and restart changed containers
#   ./scripts/deploy.sh <svc>    — pull and restart a single service
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_DIR"

if [ ! -f "$COMPOSE_FILE" ]; then
  echo "ERROR: $COMPOSE_FILE not found in $PROJECT_DIR" >&2
  exit 1
fi

if [ $# -gt 0 ]; then
  SERVICE="$1"
  echo "Pulling image for $SERVICE..."
  docker compose -f "$COMPOSE_FILE" pull "$SERVICE"
  echo "Restarting $SERVICE..."
  docker compose -f "$COMPOSE_FILE" up -d "$SERVICE"
else
  echo "Pulling all images..."
  docker compose -f "$COMPOSE_FILE" pull
  echo "Restarting changed containers..."
  docker compose -f "$COMPOSE_FILE" up -d
fi

echo "Pruning unused images..."
docker image prune -f

echo "Current status:"
docker compose -f "$COMPOSE_FILE" ps

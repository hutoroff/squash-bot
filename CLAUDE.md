# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Telegram bot for managing squash game coordination among friends. The bot handles game announcements, player registration via inline buttons ("I'm in"/"I'll skip"), guest management, capacity validation, dynamic group tracking, and automated cleanup.

## Architecture

### Technology Stack
- **Go 1.21+** with `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- **PostgreSQL 15+** with `github.com/jackc/pgx/v5`
- **Scheduling**: `github.com/robfig/cron/v3`
- **Logging**: `slog` (INFO for business events, DEBUG for technical details)
- **Config**: Environment-based with `github.com/caarlos0/env/v10`

### Core Architecture Pattern
```
telegram-squash-bot  →  HTTP API  →  squash-games-management  →  PostgreSQL
```

Two independent binaries in one Go module (`github.com/vkhutorov/squash_bot`):

**`squash-games-management`** (`cmd/squash-games-management/`)
- **api/**: HTTP handlers (REST JSON API on port 8080)
- **service/**: Business logic for games, participation, guests, scheduling
- **storage/**: SQL repositories (games, players, participations, guests, groups)
- Runs the cron scheduler; sends Telegram messages directly via `tgbotapi`

**`telegram-squash-bot`** (`cmd/telegram-squash-bot/`)
- **telegram/**: Bot loop, callback handlers, slash commands, message formatting
- **client/**: Typed HTTP client that calls the management service API
- No DB access; all data operations go through HTTP

**Shared**
- **models/**: Game, Player, GameParticipation, GuestParticipation, Group (all with JSON tags)

### Database Schema
- `games`: id, chat_id, message_id, game_date, courts_count, courts, notified_day_before, completed, created_at
- `players`: id, telegram_id (UNIQUE), username, first_name, last_name, created_at
- `game_participations`: id, game_id, player_id, status ('registered'|'skipped'), created_at, UNIQUE(game_id, player_id)
- `guest_participations`: id, game_id, invited_by_player_id, created_at
- `bot_groups`: chat_id PK, title, bot_is_admin, added_at

## Development Commands

```bash
# Start full stack
docker-compose up --build

# Start only DB (for local dev)
docker-compose up -d postgres

# Run management service locally
go run cmd/squash-games-management/main.go

# Run telegram bot locally
go run cmd/telegram-squash-bot/main.go

# Testing
go test ./...
go test -tags integration -timeout 120s ./...

# Build both binaries
go build ./cmd/squash-games-management/
go build ./cmd/telegram-squash-bot/
```

## Key Business Logic Workflows

### Button Click Flow
1. Parse callback data (`action:game_id`, e.g. `join:123`, `skip:123`)
2. Update `game_participations` table (upsert player, add/remove guest)
3. Query current participants and guests for the game
4. Format message with updated participant list + "Last updated" footer
5. Edit Telegram message in place (preserving buttons and pin status)
6. Log action at INFO level

### Scheduled Tasks (Cron-based)
- **Day Before Game**: Check participant count vs courts×2; notify if off; use `notified_day_before` flag to prevent duplicates
- **Day After Game**: Unpin message, remove InlineKeyboardMarkup, mark game complete
- **Weekly Reminder**: DM group admins on Monday morning if no game is scheduled within 7 days

### Admin & Group Management
- **Group admin rights** are verified dynamically per group via `GetChatAdministrators` — no hardcoded IDs; this controls game creation, player/guest management, and all `/games` actions
- **Service admin access** (`SERVICE_ADMIN_IDS` env var) is a separate, operator-configured set of Telegram user IDs that grants access to `/trigger` only; it is independent of group membership or Telegram admin status
- `my_chat_member` events track when the bot is added/removed/promoted/demoted
- If added without admin rights, bot DMs the user who added it

### Guest Management
- Players can add guests (+1) linked to their player record
- Players can remove their own most-recently-added guest
- Admins can remove any guest via the `/games` management menu

### Message Formatting
- Emoji header, game date/time, court list, numbered player list, guest list
- Capacity line: `courts_count × 2`
- "Last updated: [timestamp]" footer
- "Game completed ✓" marker for finished games

## Environment Variables

**`squash-games-management`:**
```
DATABASE_URL=                # required
TELEGRAM_BOT_TOKEN=          # required (scheduler sends Telegram messages)
SERVER_PORT=8080             # default 8080
CRON_DAY_BEFORE=0 20 * * *  # default 8 PM daily
CRON_DAY_AFTER=0 8 * * *    # default 8 AM daily
CRON_WEEKLY_REMINDER=0 10 * * 1  # default Monday 10 AM
LOG_LEVEL=INFO
TIMEZONE=UTC
```

**`telegram-squash-bot`:**
```
TELEGRAM_BOT_TOKEN=          # required
MANAGEMENT_SERVICE_URL=      # required (e.g. http://squash-games-management:8080)
LOG_LEVEL=INFO
TIMEZONE=UTC
SERVICE_ADMIN_IDS=           # optional; comma-separated Telegram user IDs for /trigger
```

## Testing Approach
- Unit tests for services and message formatting
- Integration tests for storage layer (requires test DB via `docker-compose.test.yml`)
- `go test -tags integration` flag gates integration tests

## Documentation Responsibility

When you make code changes, update the affected documentation **in the same task**:

| What changed | Update |
|---|---|
| New feature, command, or user-visible behavior | `README.md` |
| Env variables, setup, operator-facing config | `README.md` |
| Architecture, packages, working conventions | `AGENTS.md` |
| Business logic, DB schema, callback format, key workflows | `CLAUDE.md` (this file) |

Only edit sections that are affected. Do not rewrite correct sections.

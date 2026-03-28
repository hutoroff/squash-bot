# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Telegram bot for managing squash game coordination among friends. The bot handles game announcements, player registration via buttons ("I'm in"/"I'll skip"), capacity validation, and automated cleanup.

## Architecture

### Technology Stack
- **Go 1.21+** with `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- **PostgreSQL 15+** with `github.com/jackc/pgx/v5`
- **Scheduling**: `github.com/robfig/cron/v3` for day-before/after tasks
- **Logging**: `slog` (structured logging with INFO for business events, DEBUG for technical details)
- **Config**: Environment-based with `github.com/caarlos0/env/v10`

### Core Architecture Pattern
```
Telegram Handlers → Services → Storage → PostgreSQL
```

- **telegram/**: Handles bot API interactions, callback queries, message formatting
- **service/**: Business logic for games, players, participation, scheduling
- **storage/**: Database repositories with type-safe SQL operations
- **models/**: Core entities (Game, Player, GameParticipation)

### Database Schema
Three core tables with clear relationships:
- `games`: stores game info, message_id, chat_id, courts_count, scheduling flags
- `players`: telegram user data (telegram_id, username, names)
- `game_participations`: many-to-many link with status (registered/skipped)

## Development Commands

```bash
# Start development environment
docker-compose up -d postgres
go run cmd/bot/main.go

# Run with live reload during development
air # if using cosmtrek/air

# Database operations
go run migrations/migrate.go up
go run migrations/migrate.go down 1

# Testing
go test ./...
go test ./internal/service -v
go test -run TestGameService_AddParticipant

# Build
go build -o bin/squash_bot cmd/bot/main.go

# Docker development
docker-compose up --build
```

## Key Business Logic Workflows

### Button Click Flow (Critical)
1. Parse callback data to extract game_id and action
2. Update `game_participations` table (add/remove player)
3. Query current participants for the game
4. Format message with updated participant list + "Last updated" footer
5. Edit Telegram message with new text (preserving buttons)
6. Log action at INFO level

### Scheduled Tasks (Cron-based)
- **Day Before Game**: Check participant count vs courts*2, send notifications if needed
- **Day After Game**: Unpin message, remove buttons (edit message to remove InlineKeyboardMarkup), mark game complete

### Message Formatting Pattern
Game messages include:
- Game date/time and court info
- Numbered participant list
- "Last updated: [timestamp]" footer
- Inline keyboard with "I'm in" / "I'll skip" buttons

## Important Implementation Notes

### Callback Data Format
Use format like `action:game_id` (e.g., "join:123", "skip:123") for button callbacks.

### Message Updates
Always use `EditMessageText` to update existing messages - never send new ones. This preserves the message thread and pin status.

### Logging Strategy
- **INFO**: Button clicks, game creation, participant changes, day-before notifications, cleanup actions
- **DEBUG**: Database queries, Telegram API calls, message IDs, SQL parameters

### Error Handling
Focus on graceful degradation - if message edit fails, log error but don't crash. Bot should continue processing other requests.

### Day-Before Logic
Only send notifications if participant count != courts*2. Use `notified_day_before` flag to prevent duplicate notifications.

## Environment Variables Required
```
TELEGRAM_BOT_TOKEN=
DATABASE_URL=
GROUP_CHAT_IDS=-1001234567890,-1009876543210  # comma-separated list of group chat IDs
CRON_DAY_BEFORE=0 20 * * *  # 8 PM daily
CRON_DAY_AFTER=0 8 * * *    # 8 AM daily
LOG_LEVEL=INFO
```

## Testing Approach
- Unit tests for services (business logic, message formatting)
- Integration tests for storage layer with test database
- Manual Telegram testing in development group
- Mock Telegram API for automated testing

## Docker Setup
Use multi-stage build: builder stage for dependencies, final stage with minimal runtime. Include health checks for both bot and database in docker-compose.yml.
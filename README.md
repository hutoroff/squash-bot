# Squash Bot

A Telegram bot for coordinating squash games among a group of friends. The bot posts game announcements, lets players register with inline buttons, tracks capacity, and cleans up after each game.

## What It Does

- Admin sends a message to the bot with game details (date, time, courts)
- Bot posts a formatted announcement to the group chat and pins it
- Players tap "I'm in" or "I'll skip" — the message updates in place
- The night before the game the bot notifies if the headcount is off (too few or too many players)
- The morning after the game the bot unpins the message, removes buttons, and marks the game complete

## Tech Stack

| Component     | Technology                                    |
|---------------|-----------------------------------------------|
| Language      | Go 1.21+                                      |
| Database      | PostgreSQL 15                                 |
| Telegram API  | go-telegram-bot-api v5                        |
| DB Driver     | pgx v5 (connection pool)                      |
| Scheduling    | robfig/cron v3                                |
| Migrations    | golang-migrate                                |
| Config        | caarlos0/env (env vars → struct)              |
| Logging       | slog (structured, levelled)                   |
| Deployment    | Docker + Docker Compose                       |

## Architecture

```
Telegram (long-polling)
        ↓
  telegram/handlers.go      — parses messages & callbacks
        ↓
  service/                  — business logic
        ↓
  storage/                  — SQL repositories
        ↓
     PostgreSQL
```

Three main packages under `internal/`:

- **telegram** — bot loop, message/callback handlers, message formatting
- **service** — game creation, participation logic, scheduler tasks
- **storage** — typed SQL repositories for games, players, participations

## Quick Start

### Prerequisites

- Docker & Docker Compose
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- The bot added as an admin to your group (so it can pin messages)

### 1. Configure environment

```bash
cp .env.example .env
```

Edit `.env` and fill in the required values:

```env
TELEGRAM_BOT_TOKEN=   # from @BotFather
DATABASE_URL=postgres://squash_bot:squash_bot@postgres:7432/squash_bot?sslmode=disable
GROUP_CHAT_ID=        # your group's chat ID (negative number, e.g. -1001234567890)
ADMIN_USER_ID=        # your Telegram user ID
TIMEZONE=Europe/Moscow
```

To find your `GROUP_CHAT_ID`, add [@userinfobot](https://t.me/userinfobot) or [@RawDataBot](https://t.me/RawDataBot) to the group.

### 2. Start

```bash
docker-compose up --build
```

Migrations run automatically on startup.

### 3. Create a game

Send a private message to your bot in this exact format:

```
2025-06-15 18:00
courts: 2,3,4
```

The bot will post and pin the announcement in the group.

## Running Locally (without Docker)

```bash
# Start only the database
docker-compose up -d postgres

# Run the bot
export PATH="/opt/homebrew/bin:$PATH"   # if Go installed via Homebrew on macOS
go run cmd/bot/main.go
```

### Database migrations

```bash
go run migrations/migrate.go up       # apply all pending migrations
go run migrations/migrate.go down 1   # roll back one migration
```

## Testing

```bash
go test ./...                                      # all tests
go test ./internal/service -v                      # service tests with verbose output
go test -run TestGameService_AddParticipant        # single test
```

## Environment Variables

| Variable          | Required | Default          | Description                              |
|-------------------|----------|------------------|------------------------------------------|
| `TELEGRAM_BOT_TOKEN` | Yes   | —                | Bot token from @BotFather                |
| `DATABASE_URL`    | Yes      | —                | PostgreSQL connection string             |
| `GROUP_CHAT_ID`   | Yes      | —                | Telegram group where games are announced |
| `ADMIN_USER_ID`   | Yes      | —                | Telegram user ID allowed to create games |
| `CRON_DAY_BEFORE` | No       | `0 20 * * *`     | When to run day-before capacity check    |
| `CRON_DAY_AFTER`  | No       | `0 8 * * *`      | When to run post-game cleanup            |
| `LOG_LEVEL`       | No       | `INFO`           | `INFO` or `DEBUG`                        |
| `TIMEZONE`        | No       | `Europe/Moscow`  | Timezone for dates in messages           |

## Scheduled Tasks

| Task             | Default time | What it does                                             |
|------------------|--------------|----------------------------------------------------------|
| Day-before check | 8 PM daily   | Checks if player count matches court capacity, notifies if not |
| Day-after cleanup | 8 AM daily  | Unpins message, removes buttons, marks game complete     |

Capacity per game = `courts_count × 2`.

## Project Structure

```
cmd/bot/          — entry point
internal/
  config/         — env-based config
  models/         — Game, Player, GameParticipation
  storage/        — SQL repositories
  service/        — business logic + scheduler
  telegram/       — bot loop, handlers, formatter
migrations/       — SQL migration files
```

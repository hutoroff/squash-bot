# Squash Bot

A Telegram bot for coordinating squash games among a group of friends. The bot posts game announcements, lets players register with inline buttons, tracks capacity, and cleans up after each game.

## What It Does

- Admin creates a game via `/new_game` in private chat or by @mentioning the bot in the group
- Bot posts a formatted announcement to the group chat and pins it
- Players tap "I'm in" or "I'll skip" — the message updates in place
- Players can add guests (+1) linked to their name
- The night before the game the bot notifies if the headcount is off (too few or too many players)
- The morning after the game the bot unpins the message, removes buttons, and marks the game complete
- The bot sends a weekly reminder to admins if no game is scheduled for the upcoming week

## Tech Stack

| Component     | Technology                                    |
|---------------|-----------------------------------------------|
| Language      | Go 1.21+                                      |
| Database      | PostgreSQL 15                                 |
| Telegram API  | go-telegram-bot-api v5                        |
| DB Driver     | pgx v5 (connection pool)                      |
| Scheduling    | robfig/cron v3                                |
| Migrations    | golang-migrate (embedded SQL)                 |
| Config        | caarlos0/env (env vars → struct)              |
| Logging       | slog (structured, levelled)                   |
| Deployment    | Docker + Docker Compose                       |

## Architecture

```
telegram-squash-bot  →  HTTP API  →  squash-games-management  →  PostgreSQL
```

Two independently deployable binaries in one Go module:

- **squash-games-management** — REST API (port 8080), business logic, SQL repositories, cron scheduler; sends Telegram messages for scheduled notifications
- **telegram-squash-bot** — long-polling bot loop, message/callback handlers, slash commands; all data operations go through HTTP calls to the management service

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
TIMEZONE=UTC
```

`DATABASE_URL` is pre-configured in `docker-compose.yml` for the management service container and does not need to be in `.env` when running via Docker Compose.

### 2. Start

```bash
docker-compose up --build
```

Migrations run automatically on startup.

### 3. Add the bot to your group

Add the bot to a Telegram group and grant it admin rights (required for pinning messages). The bot will register the group automatically and start accepting game creation requests from group admins.

### 4. Create a game

In private chat with the bot, run `/new_game` and follow the prompt, or send the date/time and courts directly:

```
2025-06-15 18:00
courts: 2,3,4
```

If you are an admin in multiple groups, the bot will ask you to pick a group.

## Running Locally (without Docker)

```bash
# Start only the database
docker-compose up -d postgres

# Run the management service (in one terminal)
export PATH="/opt/homebrew/bin:$PATH"   # if Go installed via Homebrew on macOS
DATABASE_URL=postgres://squash_bot:squash_bot@localhost:7432/squash_bot \
  go run cmd/squash-games-management/main.go

# Run the telegram bot (in another terminal)
MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  go run cmd/telegram-squash-bot/main.go
```

## Testing

```bash
go test ./...                                      # all tests
go test -tags integration -timeout 120s ./...      # integration tests (requires test DB)
```

## Bot Commands

| Command     | Who can use     | Description                                      |
|-------------|-----------------|--------------------------------------------------|
| `/start`    | Anyone          | Show welcome message                             |
| `/help`     | Anyone          | List available commands                          |
| `/my_game`  | Anyone          | Show your next registered game with a link       |
| `/games`    | Group admins    | List upcoming games you manage; edit/manage them |
| `/new_game` | Group admins    | Create a new game for your group                 |
| `/trigger`  | Service admins  | Manually fire a scheduled event (private chat only); requires `SERVICE_ADMIN_IDS` |

## Guest Management

Any group member can add a guest to a game by tapping the "+1 Guest" button. Each guest entry is linked to the player who invited them and is displayed as "+1 (invited by @username)". Players can remove their own most-recently-added guest. Admins can remove any specific guest via the `/games` management menu.

Guest spots count toward capacity.

## Environment Variables

### squash-games-management

| Variable               | Required | Default           | Description                                         |
|------------------------|----------|-------------------|-----------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`   | Yes      | —                 | Bot token from @BotFather (used by the scheduler to send messages) |
| `DATABASE_URL`         | Yes      | —                 | PostgreSQL connection string                        |
| `SERVER_PORT`          | No       | `8080`            | HTTP API listen port                                |
| `CRON_DAY_BEFORE`      | No       | `0 20 * * *`      | When to run day-before capacity check               |
| `CRON_DAY_AFTER`       | No       | `0 8 * * *`       | When to run post-game cleanup                       |
| `CRON_WEEKLY_REMINDER` | No       | `0 10 * * 1`      | When to send weekly reminder to admins (Mon 10 AM)  |
| `LOG_LEVEL`            | No       | `INFO`            | `INFO` or `DEBUG`                                   |
| `TIMEZONE`             | No       | `UTC`             | Timezone for dates in messages                      |

### telegram-squash-bot

| Variable                 | Required | Default           | Description                                         |
|--------------------------|----------|-------------------|-----------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`     | Yes      | —                 | Bot token from @BotFather                           |
| `MANAGEMENT_SERVICE_URL` | Yes      | —                 | Base URL of the management service (e.g. `http://squash-games-management:8080`) |
| `LOG_LEVEL`              | No       | `INFO`            | `INFO` or `DEBUG`                                   |
| `TIMEZONE`               | No       | `UTC`             | Timezone for dates in messages                      |
| `SERVICE_ADMIN_IDS`      | No       | _(empty)_         | Comma-separated Telegram user IDs allowed to use `/trigger` |

## Scheduled Tasks

| Task              | Default time    | What it does                                                      |
|-------------------|-----------------|-------------------------------------------------------------------|
| Day-before check  | 8 PM daily      | Checks if player count matches court capacity, notifies if not    |
| Day-after cleanup | 8 AM daily      | Unpins message, removes buttons, marks game complete              |
| Weekly reminder   | Monday 10 AM    | DMs group admins if no game is scheduled within the next 7 days   |

Capacity per game = `courts_count × 2`.

## Group Management

The bot tracks which groups it belongs to in the database. When added to a group:

- If it has admin rights, it is immediately ready for use.
- If it does **not** have admin rights, it DMs the user who added it with instructions.

When the bot is promoted or demoted in a group, it updates its admin status accordingly. Groups are removed from the tracking table when the bot is kicked.

## Project Structure

```
cmd/
  squash-games-management/  — management service entry point
  telegram-squash-bot/      — telegram bot entry point
internal/
  config/         — env-based config (TelegramConfig + ManagementConfig)
  models/         — Game, Player, GameParticipation, GuestParticipation, Group
  storage/        — SQL repositories (games, players, participations, guests, groups)
  service/        — business logic + scheduler
  api/            — HTTP handlers for the management service REST API
  client/         — typed HTTP client used by the telegram bot
  telegram/       — bot loop, handlers, commands, formatter
migrations/       — embedded SQL migration files
tests/            — integration and e2e tests
.github/
  workflows/      — CI pipeline
```

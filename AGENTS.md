# AGENTS.md

This file provides working instructions for coding agents in this repository.

## Project Overview

`squash_bot` is a Go-based Telegram bot for coordinating squash games. It creates and updates pinned game announcements in a group chat, tracks participation, runs scheduled checks, and cleans up completed games.

## Stack

- Go 1.21+
- PostgreSQL 15+
- `go-telegram-bot-api/v5`
- `pgx/v5`
- `robfig/cron/v3`
- `slog`
- Docker Compose for local infrastructure

## Architecture

Primary flow:

`telegram handlers -> service layer -> storage layer -> PostgreSQL`

Key directories:

- `cmd/bot` — application entry point
- `internal/config` — environment-driven config
- `internal/models` — core domain models
- `internal/storage` — SQL repositories
- `internal/service` — business logic and scheduled jobs
- `internal/telegram` — bot handlers, callbacks, message formatting
- `migrations` — database migrations and migration runner
- `tests` — integration or higher-level tests

## Working Conventions

- Keep changes minimal and consistent with the existing Go style.
- Prefer fixing root causes over adding defensive patches around symptoms.
- Preserve the current package boundaries: transport logic in `internal/telegram`, business rules in `internal/service`, persistence in `internal/storage`.
- Do not introduce new dependencies unless clearly necessary.
- Use structured logging patterns already present in the codebase.
- Avoid unrelated refactors while implementing a task.

## Telegram Bot Rules

- Update announcement messages in place; do not replace them with new messages unless the feature explicitly requires it.
- Preserve inline keyboard behavior when changing participation flows.
- Treat callback data format as stable unless the task requires a coordinated change.
- Keep scheduling behavior idempotent where possible to avoid duplicate notifications or cleanup actions.

## Data And Config Notes

- Main configuration is environment-variable based via `.env`.
- Required runtime values include `TELEGRAM_BOT_TOKEN`, `DATABASE_URL`, `GROUP_CHAT_ID`, and `ADMIN_USER_ID`.
- Local development typically uses Docker Compose for PostgreSQL.

## Common Commands

```bash
# Start database only
docker-compose up -d postgres

# Run the bot locally
go run cmd/bot/main.go

# Run migrations
go run migrations/migrate.go up
go run migrations/migrate.go down 1

# Run tests
go test ./...
go test ./internal/service -v

# Build binary
go build -o bin/squash_bot cmd/bot/main.go
```

## Testing Guidance

- Prefer targeted tests first for the package being changed, then broaden to `go test ./...` if needed.
- Add tests when modifying business logic if there is an existing nearby test pattern.
- Do not attempt to fix unrelated failing tests unless the user asks.

## Documentation Guidance

- Update `README.md` when setup steps, commands, configuration, or behavior visible to operators changes.
- Keep documentation concise and operationally useful.

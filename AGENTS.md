# AGENTS.md

This file provides working instructions for coding agents in this repository.

## Project Overview

`squash_bot` is a Go-based Telegram bot for coordinating squash games. It creates and updates pinned game announcements in a group chat, tracks participation (including guests), runs scheduled checks, manages bot group membership dynamically, and cleans up completed games.

## Stack

- Go 1.21+
- PostgreSQL 15+
- `go-telegram-bot-api/v5`
- `pgx/v5`
- `robfig/cron/v3`
- `slog`
- Docker Compose for local infrastructure

## Architecture

Four independently deployable binaries in one Go module:

```
telegram-squash-bot  →  HTTP API  →  squash-games-management  →  PostgreSQL
sports-booking-service  →  eversports.de  (reverse-engineered cookie-auth API)
squash-web           →  HTTP API  →  squash-games-management
```

Key directories:

- `cmd/squash-games-management` — management service entry point
- `cmd/telegram-squash-bot` — telegram bot entry point
- `cmd/sports-booking-service` — sports booking service entry point
- `cmd/squash-web` — web UI entry point
- `internal/config` — environment-driven config (`TelegramConfig` / `ManagementConfig` / `BookingConfig` / `WebConfig`)
- `internal/i18n` — localisation: `Lang` type, `Normalize()`, `Localizer` (T/Tf/FormatGameDate/FormatUpdatedAt), translation maps for en/de/ru
- `internal/models` — core domain models (Game, Player, GameParticipation, GuestParticipation, Group, Venue, PlayerGame)
- `internal/storage` — SQL repositories (games, players, participations, guests, groups)
- `internal/gameformat` — shared game message formatter and keyboard builder (`FormatGameMessage`, `GameKeyboard`, `PlayerDisplayName`); used by both the telegram bot and the management service scheduler
- `internal/service` — business logic and scheduled jobs; includes `GameNotifier` for on-demand Telegram message editing triggered from the web API
- `internal/api` — HTTP handlers for the management service REST API
- `internal/client` — typed HTTP client used by the telegram bot
- `internal/telegram` — bot handlers, callbacks, slash commands, message formatting
- `internal/eversports` — reverse-engineered Eversports.de HTTP client (login, bookings, single match)
- `internal/booking` — HTTP server and handlers for the sports-booking-service REST API
- `internal/webserver` — HTTP server, SPA handler, Telegram Login Widget auth, JWT session management, and web API handlers for squash-web
- `web/embed.go` — embeds `web/frontend/dist` into the Go binary; `go generate ./web/...` runs `npm ci && npm run build`
- `web/frontend` — React + TypeScript SPA (Vite); `src/types.ts` for shared types, `src/api/` for API clients, `src/components/` for UI components
- `migrations` — embedded SQL migrations
- `tests` — integration and e2e tests
- `.github/workflows` — CI pipeline and automated documentation updates
- `.github/scripts` — helper scripts used by workflows

## Working Conventions

- Keep changes minimal and consistent with the existing Go style.
- Prefer fixing root causes over adding defensive patches around symptoms.
- Preserve the current package boundaries: transport/bot logic in `internal/telegram`, HTTP API in `internal/api`, HTTP client in `internal/client`, business rules in `internal/service`, persistence in `internal/storage`.
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
- `squash-games-management` requires `TELEGRAM_BOT_TOKEN`, `DATABASE_URL`, and `INTERNAL_API_SECRET`.
- `telegram-squash-bot` requires `TELEGRAM_BOT_TOKEN`, `MANAGEMENT_SERVICE_URL`, and `INTERNAL_API_SECRET`.
- `sports-booking-service` requires `EVERSPORTS_EMAIL`, `EVERSPORTS_PASSWORD`, and `INTERNAL_API_SECRET`. It has no database; session state is held in an in-memory cookie jar.
- `squash-web` requires `TELEGRAM_BOT_TOKEN`, `TELEGRAM_BOT_NAME`, `MANAGEMENT_SERVICE_URL`, `INTERNAL_API_SECRET`, and `JWT_SECRET`. Serves the React SPA on port 8082. The frontend is embedded in the binary; build it first with `go generate ./web/...`.
- `INTERNAL_API_SECRET` is a shared secret used to authenticate all HTTP requests between services (bearer token in `Authorization` header).
- There is no `ADMIN_USER_ID` — admin rights are determined dynamically per group via `GetChatAdministrators`.
- Local development typically uses Docker Compose for PostgreSQL.

## Common Commands

```bash
# Start database only
docker-compose up -d postgres

# Run the management service locally
DATABASE_URL=postgres://squash_bot:squash_bot@localhost:7432/squash_bot \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/squash-games-management/main.go

# Run the telegram bot locally
MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/telegram-squash-bot/main.go

# Run the sports-booking-service locally
EVERSPORTS_EMAIL=<email> \
  EVERSPORTS_PASSWORD=<password> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/sports-booking-service/main.go

# Build the React frontend (required before running squash-web locally)
go generate ./web/...

# Run squash-web locally
TELEGRAM_BOT_TOKEN=<token> \
  TELEGRAM_BOT_NAME=<bot_username> \
  MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  INTERNAL_API_SECRET=<secret> \
  JWT_SECRET=$(openssl rand -hex 32) \
  go run cmd/squash-web/main.go

# Run Go tests
go test ./...
go test -tags integration -timeout 120s ./...

# Run frontend tests
cd web/frontend && npm test

# Build binaries
go build ./cmd/squash-games-management/
go build ./cmd/telegram-squash-bot/
go build ./cmd/sports-booking-service/
```

## Testing Guidance

- Prefer targeted tests first for the package being changed, then broaden to `go test ./...` if needed.
- Add tests when modifying business logic if there is an existing nearby test pattern.
- Do not attempt to fix unrelated failing tests unless the user asks.

### Frontend tests

Tests live alongside their components (`src/components/*.test.tsx`) and use **Vitest + Testing Library**.

Key setup notes:
- `globals: true` in `vite.config.ts` is required — Testing Library registers its `afterEach(cleanup)` using the global `afterEach` at module init time; without it, DOM leaks across tests and causes spurious failures.
- `vi.mock('../api/games', factory)` keeps the `ApiError` class inline alongside `vi.fn()` stubs so tests can import and `instanceof`-check it.
- When the same text appears in both a section heading and a badge (e.g. "Upcoming"), use `getByRole('heading', { name: '...' })` instead of `getByText` to avoid ambiguous-match errors.
- Test files are excluded from `tsconfig.json` (`"exclude"`) so `tsc && vite build` doesn't type-check them; vitest uses esbuild for transformation.

## Documentation Guidance

Update documentation as part of the same task whenever code changes affect any of the following:

| What changed | Update |
|---|---|
| New or removed feature, command, or bot behavior | `README.md` |
| Setup steps, env variables, or operator-facing config | `README.md` |
| Package structure, architectural decisions, or working conventions | `AGENTS.md` |
| Business logic, DB schema, callback format, or key workflows | `CLAUDE.md` |

Rules:
- Only edit sections that are actually affected — do not rewrite correct sections.
- Keep prose concise and operationally useful.
- Update tables and lists in place; preserve existing formatting.
- If a doc section becomes inaccurate because of your change, it is your responsibility to fix it before finishing the task.

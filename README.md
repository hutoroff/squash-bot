# Squash Bot

A Telegram bot for coordinating squash games among a group of friends. The bot posts game announcements, lets players register with inline buttons, tracks capacity, and cleans up after each game.

## What It Does

- Admin creates a game via `/newgame` in private chat using a step-by-step wizard (date picker → group → venue → courts → time)
- Admin manages venues (courts, time slots, address) for their group via `/venues`
- Bot posts a formatted announcement to the group chat and pins it
- Players tap "I'm in" or "I'll skip" — the message updates in place
- Players can add guests (+1) linked to their name
- The night before the game the bot auto-cancels unused courts (if `SPORTS_BOOKING_SERVICE_URL` is set) and notifies the group with the outcome
- At midnight when booking opens, the bot auto-books courts for the preferred time (if `SPORTS_BOOKING_SERVICE_URL` and `preferred_game_time` are configured) and notifies the group
- At 10 AM on configured game days, the bot DMs group admins when court booking opens (or posts a group message if auto-booking already ran)
- The morning after the game the bot unpins the message, removes buttons, and marks the game complete
- **squash-web** provides a React web UI (port 8082): sign in with your Telegram account, browse upcoming and past games, and manage your participation (join, skip, add/remove a guest) — changes sync to the Telegram announcement in real time. Past games are shown in a collapsed section that loads on demand.

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
                                  →  sports-booking-service   →  eversports.de
squash-web           →  HTTP API  →  squash-games-management
```

Four independently deployable binaries in one Go module:

- **squash-games-management** — REST API (port 8080), business logic, SQL repositories, cron scheduler; sends Telegram messages for scheduled notifications
- **telegram-squash-bot** — long-polling bot loop, message/callback handlers, slash commands; all data operations go through HTTP calls to the management service
- **sports-booking-service** — REST API (port 8081) that wraps the Eversports website; auto-authenticates and supports listing, creating, and cancelling court bookings
- **squash-web** — web UI (port 8082); Go backend serving an embedded React SPA

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
TELEGRAM_BOT_TOKEN=     # from @BotFather
TELEGRAM_BOT_NAME=      # bot username without @ (e.g. SquashBot)
INTERNAL_API_SECRET=    # shared secret between services — generate with: openssl rand -hex 32
JWT_SECRET=             # secret for web session tokens — generate with: openssl rand -hex 32
TIMEZONE=UTC
```

`DATABASE_URL` and `MANAGEMENT_SERVICE_URL` are pre-configured in `docker-compose.yml` for the relevant containers and do not need to be in `.env` when running via Docker Compose.

### 2. Start

```bash
docker-compose up --build
```

Migrations run automatically on startup.

### 3. Add the bot to your group

Add the bot to a Telegram group and grant it admin rights (required for pinning messages). The bot will register the group automatically and start accepting game creation requests from group admins.

### 4. Configure venues

In private chat with the bot, run `/venues`. You can add one or more venues for your group. Each venue stores:
- **Name**, **courts** (comma-separated), **time slots** (preset HH:MM options), **address** (optional)
- **Game days** — weekdays when games are played (toggle keyboard; press Confirm with nothing selected to skip). Used for booking and auto-booking reminders.
- **Preferred game time** — one of the configured time slots marked as the default (highlighted ⭐ in the new-game wizard). Used by auto-booking to pick the target slot at midnight.
- **Auto-booking courts** — ordered subset of courts tried first when auto-booking at midnight (priority order). Leave blank to book any available court.
- **Grace period** — hours before the game when the cancellation reminder fires (default 24h).
- **Booking opens (days)** — how many days ahead court booking opens (default 14). Shown in the booking reminder DM and used by auto-booking to compute the target date.

**At least one venue must be configured before you can create games.** Once venues are set up, the game creation wizard uses them for guided court and time selection.

### 5. Create a game

In private chat with the bot, run `/newgame`. The bot will guide you through a wizard:

**Single-group admin:**
1. **Pick a date** — tap one of the date buttons (today + next 13 days)
2. **Select a venue** — skipped automatically if only one venue exists
3. **Toggle courts** — tap courts to select/deselect, then confirm
4. **Select a time slot** — or tap "Custom time" to type a time manually

**Multi-group admin:**
1. **Pick a date** — tap one of the date buttons
2. **Pick a group** — choose which group to post the game in
3. **Select a venue** — skipped automatically if only one venue exists for that group
4. **Toggle courts** — tap courts to select/deselect, then confirm
5. **Select a time slot** — or tap "Custom time" to type a time manually

If the selected group has no venues configured, the wizard shows an error and you can pick a different group or add venues first via `/venues`.

## Running Locally (without Docker)

```bash
# Start only the database
docker-compose up -d postgres

# Run the management service (in one terminal)
export PATH="/opt/homebrew/bin:$PATH"   # if Go installed via Homebrew on macOS
DATABASE_URL=postgres://squash_bot:squash_bot@localhost:7432/squash_bot \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/squash-games-management/main.go

# Run the telegram bot (in another terminal)
MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/telegram-squash-bot/main.go
```

### squash-web

The Go backend embeds the compiled React frontend from `web/frontend/dist/`. Build the frontend once before running the Go binary locally — or any time the frontend source changes:

```bash
# Build the frontend (runs npm ci + vite build inside web/frontend)
go generate ./web/...

# Run the web service
TELEGRAM_BOT_TOKEN=<token> \
  TELEGRAM_BOT_NAME=<bot_username_without_@> \
  MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  INTERNAL_API_SECRET=<secret> \
  JWT_SECRET=$(openssl rand -hex 32) \
  go run cmd/squash-web/main.go
# → http://localhost:8082
```

For faster frontend iteration, run the Vite dev server instead:

```bash
cd web/frontend && npm run dev   # hot-reload dev server on http://localhost:5173
```

The Vite dev server talks directly to the browser; the Go backend is not involved during frontend development.

#### Telegram Login Widget — BotFather domain setup

The Login Widget only works on domains that are explicitly registered with Telegram. This is a one-time step per deployment:

1. Open [@BotFather](https://t.me/BotFather) and send `/mybots`.
2. Select your bot → **Bot Settings → Domain**.
3. Enter the **hostname only** of your squash-web deployment — no `https://` prefix, no path (e.g. `squash.example.com`).

> **Local development:** `localhost` is not accepted by the Telegram Login Widget. Use a tunnel such as [ngrok](https://ngrok.com/) (`ngrok http 8082`), register the generated hostname in BotFather, and set `TELEGRAM_BOT_NAME` accordingly before testing the login flow end-to-end.

## Testing

```bash
go test ./...                                      # all Go tests
go test -tags integration -timeout 120s ./...      # integration tests (requires test DB)

# Frontend tests (Vitest + Testing Library)
cd web/frontend && npm test
```

## Versioning & Releases

Each service has an independent version (`MAJOR.MINOR.BUILD`) stored in:
- `cmd/squash-games-management/VERSION`
- `cmd/telegram-squash-bot/VERSION`
- `cmd/sports-booking-service/VERSION`

The version is injected at build time (`-ldflags "-X main.Version=..."`) and logged on startup. Each service exposes `GET /version` returning `{"version": "1.0.0"}`. The telegram bot additionally calls `GET /version` on the management service at startup and refuses to start if the major versions differ.

### Releasing a service

Trigger the relevant workflow from **GitHub Actions → Run workflow**:

- **Release Management Service** — for `squash-games-management`
- **Release Telegram Bot** — for `telegram-squash-bot`
- **Release Booking Service** — for `sports-booking-service`

Select the bump type (`patch` / `minor` / `major`). The workflow will:

1. Verify the `build-and-test` CI job passed for the exact commit being released (fails immediately otherwise).
2. Bump the `VERSION` file.
3. Build and push Docker images tagged `<version>` and `latest` to Docker Hub and GHCR.
4. Commit the bumped `VERSION` back to the branch and create a git tag (`management/vX.Y.Z`, `telegram/vX.Y.Z`, or `booking/vX.Y.Z`).

### One-time GitHub setup

| Type     | Name                | Value                                              |
|----------|---------------------|----------------------------------------------------|
| Variable | `DOCKERHUB_USERNAME`| Docker Hub org or username for image names         |
| Secret   | `DOCKERHUB_TOKEN`   | Docker Hub access token with push rights           |
| Secret   | `RELEASE_PAT`       | Personal Access Token used by the release workflows to push the VERSION-bump commit to `main`. Required when main is branch-protected. Classic PAT: `repo` scope. Fine-grained PAT: `Contents: Read and Write`. The PAT owner must be a bypass actor in the branch-protection rule. |

`GITHUB_TOKEN` is provided automatically and is used for GHCR pushes and CI status checks.

Published image names:
```
<DOCKERHUB_USERNAME>/squash-games-management:<version>
ghcr.io/<github_owner>/squash-games-management:<version>

<DOCKERHUB_USERNAME>/sports-booking-service:<version>
ghcr.io/<github_owner>/sports-booking-service:<version>
```

## Production Deployment

The project ships a dedicated `docker-compose.prod.yml` for production that uses pre-built images from Docker Hub instead of building from source. All three services and PostgreSQL run on a single server.

### Server setup

```bash
# 1. Install Docker on a fresh VPS (e.g. Ubuntu 24.04)
apt update && apt install -y docker.io docker-compose-v2
systemctl enable docker

# 2. Create the project directory
mkdir -p /opt/squash-bot && cd /opt/squash-bot

# 3. Copy docker-compose.prod.yml and create .env from .env.example
#    Fill in all required values. Generate secrets:
openssl rand -hex 32   # → INTERNAL_API_SECRET
openssl rand -hex 32   # → POSTGRES_PASSWORD

# 4. Lock down the .env file
chmod 600 .env

# 5. Pull images and start
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

Migrations run automatically on first startup. Verify everything is healthy:

```bash
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs --tail=20
```

### Updating a service

After triggering a release workflow in GitHub Actions:

```bash
# Update the version in .env (e.g. MANAGEMENT_VERSION=1.0.2), then:
scripts/deploy.sh                        # pull all + restart changed
scripts/deploy.sh squash-games-management  # or update a single service
```

### Database backups

The `db-backup` sidecar in `docker-compose.prod.yml` runs `pg_dump` daily and retains the last 7 days in a `db_backups` Docker volume.

Verify a backup exists:
```bash
docker compose -f docker-compose.prod.yml exec db-backup ls -lh /backups/
```

To restore from the most recent backup:
```bash
# Find the latest dump inside the backup container
LATEST=$(docker compose -f docker-compose.prod.yml exec -T db-backup \
  sh -c 'ls -t /backups/squash_bot_*.dump | head -1')

# Pipe it into pg_restore on the postgres container
docker compose -f docker-compose.prod.yml exec -T db-backup cat "$LATEST" | \
  docker compose -f docker-compose.prod.yml exec -T postgres \
    pg_restore -U squash_bot -d squash_bot --clean --if-exists
```

To copy a backup to the host for safekeeping:
```bash
docker compose -f docker-compose.prod.yml cp db-backup:/backups/ ./backups/
```

### Health monitoring

`scripts/healthcheck.sh` pings the `/health` endpoints and sends a Telegram alert if a service is down. Install it in cron:

```bash
# Set the env vars for the script (use the same bot token; CHAT_ID is your personal Telegram chat ID)
export HEALTHCHECK_BOT_TOKEN=<token>
export HEALTHCHECK_CHAT_ID=<your_chat_id>

# Add to crontab (runs every 5 minutes)
crontab -e
# */5 * * * * HEALTHCHECK_BOT_TOKEN=<token> HEALTHCHECK_CHAT_ID=<id> /opt/squash-bot/scripts/healthcheck.sh
```

## Bot Commands

| Command     | Who can use     | Description                                      |
|-------------|-----------------|--------------------------------------------------|
| `/start`    | Anyone          | Show welcome message                             |
| `/help`     | Anyone          | List available commands                          |
| `/mygame`   | Anyone          | Show your next registered game with a link       |
| `/games`    | Group admins    | List upcoming games you manage; edit/manage them |
| `/newgame`  | Group admins    | Create a new game for your group (wizard)        |
| `/venues`   | Group admins    | Manage venues (courts, time slots, address, game days, preferred time, auto-booking courts, grace period, booking opens days) |
| `/language` | Group admins    | Set the bot language for a group (en/de/ru)      |
| `/trigger`  | Service admins  | Manually fire a scheduled event (private chat only); requires `SERVICE_ADMIN_IDS`. Bypasses the time-window gate for the chosen task (same-day dedup guards still apply). Events: `cancellation_reminder`, `booking_reminder`, `auto_booking`, `day_after_cleanup` |

## Localisation

The bot supports three languages: **English** (default), **German**, and **Russian**.

- **Group messages** (game announcements, capacity notifications, weekly reminders) use the language configured for that group.
- **Private messages** use the language from the user's Telegram client (`LanguageCode`), falling back to English if the language is unsupported.

Group admins set the language with `/language`. If the admin manages multiple groups, the bot first asks which group to configure, then shows the language picker. The setting is stored per group and survives bot restarts.

## Guest Management

Any group member can add a guest to a game by tapping the "+1 Guest" button. Each guest entry is linked to the player who invited them and is displayed as "+1 (invited by @username)". Players can remove their own most-recently-added guest. Admins can remove any specific guest via the `/games` management menu.

Guest spots count toward capacity.

## Environment Variables

### squash-games-management

| Variable               | Required | Default           | Description                                         |
|------------------------|----------|-------------------|-----------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`   | Yes      | —                 | Bot token from @BotFather (used by the scheduler to send messages) |
| `DATABASE_URL`         | Yes      | —                 | PostgreSQL connection string                        |
| `INTERNAL_API_SECRET`  | Yes      | —                 | Shared secret for authenticating calls from the telegram bot; generate with `openssl rand -hex 32` |
| `SERVER_PORT`          | No       | `8080`            | HTTP API listen port                                |
| `CRON_POLL`            | No       | `*/5 * * * *`     | How often to poll for scheduled tasks (every 5 min) |
| `LOG_LEVEL`            | No       | `INFO`            | `INFO` or `DEBUG`                                   |
| `TIMEZONE`             | No       | `UTC`             | Timezone for dates in messages                      |
| `SPORTS_BOOKING_SERVICE_URL` | No | _(empty)_        | Base URL of the sports-booking-service (e.g. `http://sports-booking-service:8081`); when set, enables automatic court cancellation in the cancellation reminder and automatic court booking at midnight when booking opens |
| `AUTO_BOOKING_COURTS_COUNT`  | No | `3`              | Number of courts to book automatically at midnight; requires `SPORTS_BOOKING_SERVICE_URL` |

### telegram-squash-bot

| Variable                 | Required | Default           | Description                                         |
|--------------------------|----------|-------------------|-----------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`     | Yes      | —                 | Bot token from @BotFather                           |
| `MANAGEMENT_SERVICE_URL` | Yes      | —                 | Base URL of the management service (e.g. `http://squash-games-management:8080`) |
| `INTERNAL_API_SECRET`    | Yes      | —                 | Must match the value set on the management service  |
| `LOG_LEVEL`              | No       | `INFO`            | `INFO` or `DEBUG`                                   |
| `TIMEZONE`               | No       | `UTC`             | Timezone for dates in messages                      |
| `SERVICE_ADMIN_IDS`      | No       | _(empty)_         | Comma-separated Telegram user IDs allowed to use `/trigger` |

### sports-booking-service

See [docs/sports-booking-service.md](docs/sports-booking-service.md) for the full list of environment variables, API endpoints, and local run instructions.

### squash-web

| Variable                 | Required | Default | Description                                                                                                             |
|--------------------------|----------|---------|-------------------------------------------------------------------------------------------------------------------------|
| `TELEGRAM_BOT_TOKEN`     | Yes      | —       | Bot token from @BotFather; used to verify Telegram Login Widget callbacks (HMAC-SHA256 check)                           |
| `TELEGRAM_BOT_NAME`      | Yes      | —       | Bot username **without** `@` (e.g. `SquashBot`); embedded in the Login Widget so Telegram knows which bot to authorise |
| `MANAGEMENT_SERVICE_URL` | Yes      | —       | Base URL of squash-games-management (e.g. `http://squash-games-management:8080`); pre-set in `docker-compose.yml`       |
| `INTERNAL_API_SECRET`    | Yes      | —       | Must match the value on squash-games-management; used to call `GET /api/v1/players/{id}` (login) and `GET /api/v1/players/{id}/games` (games list) |
| `JWT_SECRET`             | Yes      | —       | Signs and verifies session cookies (HS256 JWT, 7-day expiry); generate with `openssl rand -hex 32`                      |
| `SERVER_PORT`            | No       | `8082`  | HTTP listen port                                                                                                        |
| `LOG_LEVEL`              | No       | `INFO`  | `INFO` or `DEBUG`                                                                                                       |
| `TIMEZONE`               | No       | `UTC`   | Timezone for date formatting                                                                                            |

## Scheduled Tasks

A single 5-minute poll (configured via `CRON_POLL`) runs four tasks, each using per-group timezone and per-venue configuration:

| Task                       | Trigger window (cron)  | What it does                                                                          |
|----------------------------|------------------------|---------------------------------------------------------------------------------------|
| Auto-booking               | 00:00–00:05 (group TZ) | Books courts for the preferred time on configured game days when booking opens.       |
| Cancellation reminder      | ±2m30s of reminder time | Fires `grace_period_hours + 6` hours before game. Checks capacity, notifies.        |
| Booking reminder           | 10:00–10:05 (group TZ) | DMs admins on configured game days with booking info (or confirms auto-booking ran).  |
| Day-after cleanup          | 03:00–03:05 (group TZ) | Unpins message, removes buttons, marks yesterday's games complete.                    |

`/trigger <event>` bypasses the cron time-window gate for the chosen task. Same-day dedup guards (`last_auto_booking_at`, `last_booking_reminder_at`, `notified_day_before`) and `game_days` validation still apply.

**Auto-booking**: fires at midnight in each group's timezone on configured game days, for venues with `preferred_game_time` set. Deduped via `venue.last_auto_booking_at` (one per calendar day). Requires `SPORTS_BOOKING_SERVICE_URL`. Queries available (unbooked) slots in a ±10-minute window around `preferred_game_time` for the date `today + booking_opens_days`, then books up to `AUTO_BOOKING_COURTS_COUNT` courts. On full success, sends a group notification. On partial or full failure, silently DMs all group admins.

**Cancellation reminder**: fires when `now ≈ game_date - (venue.grace_period_hours + 6h)`. Deduped via `notified_day_before` flag. When `SPORTS_BOOKING_SERVICE_URL` is configured, automatically cancels fully-unused courts (each unused court has 2 empty spots) before notifying. Courts to cancel are selected in two phases: **phase 1** — if `auto_booking_courts` is configured, iterate it in reverse (lowest-priority first) and pick booked courts up to the cancel target; **phase 2** — for any remaining slots not covered by phase 1, apply a consecutive-grouping fallback: booked courts are split into runs of adjacent IDs; the smallest run is picked first (tie-break: lowest first court ID); the last court in the run is canceled. Always sends one of four notification scenarios: all good (no cancellation needed), balanced (courts canceled, all seats filled), 1 free spot (odd player count), or all canceled (game will not happen).

**Booking reminder**: fires at 10 AM in each group's timezone on configured game days (`venue.game_days`). Deduped via `venue.last_booking_reminder_at` (one per calendar day per venue). Skipped silently (without updating the dedup guard) if a game already exists for the target date (`today + booking_opens_days`). If auto-booking already ran today (`venue.last_auto_booking_at` is set), sends a group confirmation message instead of a DM. Otherwise DMs all group admins: booking is open now, and the game is in `booking_opens_days` days.

**Timezone**: set per group via `/language` → "🕐 Set Timezone" → select from curated list of 18 IANA timezones. Default is UTC.

Capacity per game = `courts_count × 2`.

## Group Management

The bot tracks which groups it belongs to in the database. When added to a group:

- If it has admin rights, it is immediately ready for use.
- If it does **not** have admin rights, it DMs the user who added it with instructions.

When the bot is promoted or demoted in a group, it updates its admin status accordingly. Groups are removed from the tracking table when the bot is kicked.

## Sports Booking Service

**sports-booking-service** is a lightweight HTTP service (port 8081) that connects to [Eversports](https://www.eversports.de/) on behalf of a configured user account. It supports listing, creating, and cancelling court bookings.

See [docs/sports-booking-service.md](docs/sports-booking-service.md) for API endpoints, environment variables, and local run instructions.

## Project Structure

```
cmd/
  squash-games-management/  — management service entry point
  telegram-squash-bot/      — telegram bot entry point
  sports-booking-service/   — Eversports booking service entry point
  squash-web/               — web UI entry point
internal/
  config/         — env-based config (TelegramConfig, ManagementConfig, BookingConfig, WebConfig)
  i18n/           — localisation (en/de/ru strings, Localizer, date formatting)
  models/         — Game, Player, GameParticipation, GuestParticipation, Group, Venue
  storage/        — SQL repositories (games, players, participations, guests, groups)
  service/        — business logic + scheduler; GameNotifier for on-demand Telegram message edits
  api/            — HTTP handlers for the management service REST API
  client/         — typed HTTP client used by the telegram bot
  telegram/       — bot loop, handlers, commands, formatter
  gameformat/     — shared game message formatter and keyboard builder (used by telegram bot and management service)
  eversports/     — Eversports HTTP client (GraphQL login/match, /api/slot for court availability, calendar HTML for court discovery)
  booking/        — HTTP server wrapping the Eversports client
  webserver/      — HTTP server + SPA handler for the web UI
web/
  embed.go        — embeds web/frontend/dist into the Go binary (go:generate builds it)
  frontend/       — React + Vite + TypeScript source; `npm run build` outputs to dist/
migrations/       — embedded SQL migration files
scripts/          — deploy.sh, healthcheck.sh (production ops)
tests/            — integration and e2e tests
.github/
  workflows/      — CI pipeline and release workflows
```

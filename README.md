# Squash Bot

A Telegram bot for coordinating squash games among a group of friends. The bot posts game announcements, lets players register with inline buttons, tracks capacity, and cleans up after each game.

## What It Does

- Admin creates a game via `/newgame` in private chat using a step-by-step wizard (date picker → group → venue → courts → time)
- Admin manages venues (courts, time slots, address) for their group via `/venues`
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
                                  →  sports-booking-service   →  eversports.de
```

Three independently deployable binaries in one Go module:

- **squash-games-management** — REST API (port 8080), business logic, SQL repositories, cron scheduler; sends Telegram messages for scheduled notifications
- **telegram-squash-bot** — long-polling bot loop, message/callback handlers, slash commands; all data operations go through HTTP calls to the management service
- **sports-booking-service** — REST API (port 8081) that wraps the Eversports website; auto-authenticates and supports listing, creating, and cancelling court bookings

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
INTERNAL_API_SECRET=    # shared secret between the two services — generate with: openssl rand -hex 32
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

### 4. Configure venues

In private chat with the bot, run `/venues`. You can add one or more venues for your group. Each venue stores:
- **Name**, **courts** (comma-separated), **time slots** (preset HH:MM options), **address** (optional)
- **Game days** — weekdays when games are played (toggle keyboard). Used for the booking reminder.
- **Grace period** — hours before the game when the cancellation reminder fires (default 24h).

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

## Testing

```bash
go test ./...                                      # all tests
go test -tags integration -timeout 120s ./...      # integration tests (requires test DB)
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

`GITHUB_TOKEN` is provided automatically and is used for GHCR pushes and CI status checks.

Published image names:
```
<DOCKERHUB_USERNAME>/squash-games-management:<version>
ghcr.io/<github_owner>/squash-games-management:<version>

<DOCKERHUB_USERNAME>/sports-booking-service:<version>
ghcr.io/<github_owner>/sports-booking-service:<version>
```

## Bot Commands

| Command     | Who can use     | Description                                      |
|-------------|-----------------|--------------------------------------------------|
| `/start`    | Anyone          | Show welcome message                             |
| `/help`     | Anyone          | List available commands                          |
| `/mygame`   | Anyone          | Show your next registered game with a link       |
| `/games`    | Group admins    | List upcoming games you manage; edit/manage them |
| `/newgame`  | Group admins    | Create a new game for your group (wizard)        |
| `/venues`   | Group admins    | Manage venues (courts, time slots, address)      |
| `/language` | Group admins    | Set the bot language for a group (en/de/ru)      |
| `/trigger`  | Service admins  | Manually fire a scheduled event (private chat only); requires `SERVICE_ADMIN_IDS` |

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

| Variable                    | Required | Default           | Description                                                    |
|-----------------------------|----------|-------------------|----------------------------------------------------------------|
| `EVERSPORTS_EMAIL`          | Yes      | —                 | Eversports account email                                       |
| `EVERSPORTS_PASSWORD`       | Yes      | —                 | Eversports account password                                    |
| `EVERSPORTS_FACILITY_ID`    | No       | _(empty)_         | Numeric facility ID required for `GET /api/v1/eversports/games` and `GET /api/v1/eversports/courts`. Find it in the venue page URL (e.g. `eversports.de/s/venue-name-76443`). |
| `EVERSPORTS_FACILITY_UUID`  | No       | `6266968c-…`      | UUID of the facility used when creating bookings via `POST /api/v1/eversports/matches`. Find it in the `facilityUuid` field of the `/checkout/api/payableitem/courtbooking` request body in browser DevTools. |
| `EVERSPORTS_SPORT_UUID`     | No       | `b388b6e6-…`      | UUID of the sport used when creating bookings (`POST /matches`), resolving courts for `GET /games`, and listing courts (`GET /courts`). Defaults to the well-known Eversports squash UUID. Only change this for non-squash sports. |
| `EVERSPORTS_FACILITY_SLUG`  | No       | _(empty)_         | Facility slug from the venue URL on eversports.de (e.g. `squash-house-berlin-03`). Required for `GET /api/v1/eversports/games` and `GET /api/v1/eversports/courts`. |
| `EVERSPORTS_SPORT_ID`       | No       | _(empty)_         | Numeric sport ID from the booking calendar (e.g. `496` for squash at your facility). Required for `GET /api/v1/eversports/games` and `GET /api/v1/eversports/courts`. Find it in the `sport[id]` field of the `/api/booking/calendar/update` request body in browser DevTools. |
| `EVERSPORTS_SPORT_SLUG`     | No       | `squash`          | Sport slug used in the calendar request body. Only change if booking a different sport. |
| `EVERSPORTS_SPORT_NAME`     | No       | `Squash`          | Sport display name used in the calendar request body. Only change if booking a different sport. |
| `EVERSPORTS_BOOKINGS_PATH`  | No       | `/user/bookings`  | URL path used by the `GET /api/v1/eversports/debug-page` diagnostic endpoint; change if your locale uses a prefix like `/de/user/bookings` |
| `INTERNAL_API_SECRET`       | Yes      | —                 | Shared secret for authenticating calls to this service         |
| `SERVER_PORT`               | No       | `8081`            | HTTP API listen port                                           |
| `LOG_LEVEL`                 | No       | `INFO`            | `INFO` or `DEBUG`                                              |
| `TIMEZONE`                  | No       | `UTC`             | Timezone for log timestamps                                    |

## Scheduled Tasks

A single 5-minute poll (configured via `CRON_POLL`) runs three tasks, each using per-group timezone and per-venue configuration:

| Task                       | Trigger window      | What it does                                                                    |
|----------------------------|---------------------|---------------------------------------------------------------------------------|
| Cancellation reminder      | Any time (±2m30s)   | Fires `grace_period_hours + 6` hours before game. Checks capacity, notifies.    |
| Booking reminder           | 10:00–10:05 (group TZ) | DMs group admins on configured game days with booking opening info.          |
| Day-after cleanup          | 03:00–03:05 (group TZ) | Unpins message, removes buttons, marks yesterday's games complete.           |

**Cancellation reminder**: fires when `now ≈ game_date - (venue.grace_period_hours + 6h)`. Deduped via `notified_day_before` flag.

**Booking reminder**: fires at 10 AM in each group's timezone on configured game days (`venue.game_days`). Deduped via `venue.last_booking_reminder_at` (one per calendar day per venue). Message includes venue name and `venue.booking_opens_days`.

**Timezone**: set per group via `/language` → "🕐 Set Timezone" → select from curated list of 18 IANA timezones. Default is UTC.

Capacity per game = `courts_count × 2`.

## Group Management

The bot tracks which groups it belongs to in the database. When added to a group:

- If it has admin rights, it is immediately ready for use.
- If it does **not** have admin rights, it DMs the user who added it with instructions.

When the bot is promoted or demoted in a group, it updates its admin status accordingly. Groups are removed from the tracking table when the bot is kicked.

## Sports Booking Service

**sports-booking-service** is a lightweight HTTP service (port 8081) that connects to [Eversports](https://www.eversports.de/) on behalf of a configured user account.

| Method | Path                                  | Auth | Description                                                    |
|--------|---------------------------------------|------|----------------------------------------------------------------|
| `GET`  | `/health`                             | No   | Liveness probe                                                 |
| `GET`  | `/version`                            | No   | Service version                                                |
| `GET`  | `/api/v1/eversports/bookings`         | Yes  | List the authenticated user's upcoming court bookings          |
| `POST` | `/api/v1/eversports/matches`          | Yes  | Create a court booking. Body: `{"courtUuid":"…","start":"…","end":"…"}` (RFC 3339). Returns `{"bookingUuid":"…","bookingId":…}`. Requires `EVERSPORTS_FACILITY_UUID` and `EVERSPORTS_SPORT_UUID`. |
| `GET`  | `/api/v1/eversports/matches/{id}`     | Yes  | Fetch a single booking by its UUID with full detail            |
| `DELETE` | `/api/v1/eversports/matches/{id}`   | Yes  | Cancel a booking by UUID. Returns `{"id":"…","state":"CANCELLED","relativeLink":"…"}`. |
| `GET`  | `/api/v1/eversports/games?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Yes | Court reservations for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means the slot is already reserved. Optionally filter by time window (inclusive) and/or by whether the authenticated user owns the reservation (`my=true\|false`). Court IDs are resolved automatically via `/courts`. Requires `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_SLUG`, `EVERSPORTS_SPORT_ID`, and `EVERSPORTS_SPORT_UUID`. |
| `GET`  | `/api/v1/eversports/courts`           | Yes  | List courts at the facility; returns `[{"id":"…","uuid":"…","name":"…"}]`. Parses the Eversports booking calendar HTML. Requires `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_SLUG`, `EVERSPORTS_SPORT_ID`, and `EVERSPORTS_SPORT_UUID`. |
| `GET`  | `/api/v1/eversports/facility`         | Yes  | Venue profile for the configured facility; returns `{"id","slug","name","rating","reviewCount","address","hideAddress","tags","contact","sports","city","company"}`. Returns 404 if the slug is not found on Eversports. Requires `EVERSPORTS_FACILITY_SLUG`. |
| `GET`  | `/api/v1/eversports/debug-page`       | Yes  | Diagnostic: fetch the bookings page and return `__NEXT_DATA__` if present |

Authentication with Eversports is handled automatically: the service logs in on the first request and re-authenticates if the session expires. Login POSTs the `LoginCredentialLogin` GraphQL mutation to `https://www.eversports.de/api/checkout`, stores the resulting `et` session cookie in an in-memory jar, then calls `GET /u/self` to retrieve the authenticated user's legacy numeric ID.

Bookings are fetched from `GET https://www.eversports.de/api/user/activities?userId=<numericID>&past=false`, where the numeric user ID is obtained automatically from `GET /u/self` after login. The response is an HTML fragment; the service parses it to extract each booking's match UUID, start/end times, sport, venue name, and court. Because the activities endpoint does not include a timezone, returned times carry a UTC location as a placeholder; use `GET /api/v1/eversports/matches/{id}` for the accurate RFC 3339 timestamp with the correct offset.

All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.

To run locally:

```bash
EVERSPORTS_EMAIL=you@example.com \
  EVERSPORTS_PASSWORD=secret \
  INTERNAL_API_SECRET=test \
  go run cmd/sports-booking-service/main.go

# Fetch bookings (service logs in automatically on first request)
curl -H "Authorization: Bearer test" http://localhost:8081/api/v1/eversports/bookings

# Fetch single booking detail (UUID from bookings list)
curl -H "Authorization: Bearer test" http://localhost:8081/api/v1/eversports/matches/<uuid>

# Create a booking
curl -X POST -H "Authorization: Bearer test" -H "Content-Type: application/json" \
  -d '{"courtUuid":"<court-uuid>","start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}' \
  http://localhost:8081/api/v1/eversports/matches

# Cancel a booking
curl -X DELETE -H "Authorization: Bearer test" http://localhost:8081/api/v1/eversports/matches/<uuid>
```

## Project Structure

```
cmd/
  squash-games-management/  — management service entry point
  telegram-squash-bot/      — telegram bot entry point
  sports-booking-service/   — Eversports booking service entry point
internal/
  config/         — env-based config (TelegramConfig, ManagementConfig, BookingConfig)
  i18n/           — localisation (en/de/ru strings, Localizer, date formatting)
  models/         — Game, Player, GameParticipation, GuestParticipation, Group, Venue
  storage/        — SQL repositories (games, players, participations, guests, groups)
  service/        — business logic + scheduler
  api/            — HTTP handlers for the management service REST API
  client/         — typed HTTP client used by the telegram bot
  telegram/       — bot loop, handlers, commands, formatter
  eversports/     — Eversports HTTP client (GraphQL login, HTML scraping for bookings)
  booking/        — HTTP server wrapping the Eversports client
migrations/       — embedded SQL migration files
tests/            — integration and e2e tests
.github/
  workflows/      — CI pipeline and release workflows
```

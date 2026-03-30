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
sports-booking-service  →  eversports.de  (reverse-engineered cookie-auth API)
```

Three independent binaries in one Go module (`github.com/vkhutorov/squash_bot`):

**`squash-games-management`** (`cmd/squash-games-management/`)
- **api/**: HTTP handlers (REST JSON API on port 8080)
- **service/**: Business logic for games, participation, guests, scheduling
- **storage/**: SQL repositories (games, players, participations, guests, groups)
- Runs the cron scheduler; sends Telegram messages directly via `tgbotapi`

**`telegram-squash-bot`** (`cmd/telegram-squash-bot/`)
- **telegram/**: Bot loop, callback handlers, slash commands, message formatting
- **client/**: Typed HTTP client that calls the management service API
- No DB access; all data operations go through HTTP

**`sports-booking-service`** (`cmd/sports-booking-service/`)
- **eversports/**: Reverse-engineered Eversports HTTP client (login, bookings list, single match)
- **booking/**: REST API wrapping the Eversports client (port 8081)
- No DB access; stateless except for the in-memory cookie jar that holds the session

**Shared**
- **models/**: Game, Player, GameParticipation, GuestParticipation, Group (all with JSON tags)
- **i18n/**: `Lang` type (`en`/`de`/`ru`), `Normalize(code)` maps Telegram `LanguageCode` to a supported lang, `Localizer` provides `T(key)`, `Tf(key, args...)`, `FormatGameDate(t)`, `FormatUpdatedAt(t)`, `FormatDayMonth(t)`, `ShortWeekday(w)`

### Database Schema
- `games`: id, chat_id, message_id, game_date, courts_count, courts, venue_id (nullable FK→venues), notified_day_before, completed, created_at
- `players`: id, telegram_id (UNIQUE), username, first_name, last_name, created_at
- `game_participations`: id, game_id, player_id, status ('registered'|'skipped'), created_at, UNIQUE(game_id, player_id)
- `guest_participations`: id, game_id, invited_by_player_id, created_at
- `bot_groups`: chat_id PK, title, bot_is_admin, language (VARCHAR(5) DEFAULT 'en'), timezone (VARCHAR(64) DEFAULT 'UTC'), added_at
- `venues`: id, group_id (FK→bot_groups), name, courts (comma-separated), time_slots (comma-separated HH:MM), address (nullable), grace_period_hours (INT DEFAULT 24), game_days (TEXT DEFAULT ''), booking_opens_days (INT DEFAULT 14), last_booking_reminder_at (TIMESTAMPTZ nullable), created_at, UNIQUE(group_id, name)

## Development Commands

```bash
# Start full stack
docker-compose up --build

# Start only DB (for local dev)
docker-compose up -d postgres

# Run management service locally
DATABASE_URL=postgres://squash_bot:squash_bot@localhost:7432/squash_bot \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/squash-games-management/main.go

# Run telegram bot locally
MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/telegram-squash-bot/main.go

# Run sports-booking-service locally
EVERSPORTS_EMAIL=<email> \
  EVERSPORTS_PASSWORD=<password> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/sports-booking-service/main.go

# Testing
go test ./...
go test -tags integration -timeout 120s ./...

# Build all binaries
go build ./cmd/squash-games-management/
go build ./cmd/telegram-squash-bot/
go build ./cmd/sports-booking-service/

# Build with explicit version (mirrors what Docker does)
go build -ldflags="-X main.Version=1.2.3" ./cmd/squash-games-management/
go build -ldflags="-X main.Version=1.2.3" ./cmd/telegram-squash-bot/
go build -ldflags="-X main.Version=1.2.3" ./cmd/sports-booking-service/
```

## Versioning & Release

Each service has its own independent version stored in a plain-text file:
- `cmd/squash-games-management/VERSION`
- `cmd/telegram-squash-bot/VERSION`
- `cmd/sports-booking-service/VERSION`

Format: `MAJOR.MINOR.BUILD` (e.g. `1.0.33`).

Version is injected at build time via `-ldflags "-X main.Version=<ver>"` and logged on startup. The management service exposes `GET /version` (unauthenticated, like `/health`) returning `{"version": "1.0.33"}`.

**API compatibility rule**: services are compatible within the same major version. The telegram bot calls `GET /version` on the management service at startup and exits with an error if the major versions differ.

### Releasing a service

Trigger the relevant GitHub Actions workflow manually:
- **Actions → Release Management Service** for `squash-games-management`
- **Actions → Release Telegram Bot** for `telegram-squash-bot`

Select bump type (`patch` / `minor` / `major`). The workflow will:
1. Verify the CI job `build-and-test` passed for the exact HEAD commit (fails immediately otherwise).
2. Bump the `VERSION` file.
3. Build and push Docker images tagged `<version>` and `latest` to both Docker Hub and GHCR.
4. Commit the bumped `VERSION` back to the branch and create a git tag (`management/vX.Y.Z` or `telegram/vX.Y.Z`).

**Required GitHub configuration (one-time setup):**
- Variable `DOCKERHUB_USERNAME` — Docker Hub org or username for image names.
- Secret `DOCKERHUB_TOKEN` — Docker Hub access token with push rights.
- `GITHUB_TOKEN` is used automatically for GHCR pushes and the check-runs API.

Image names follow the pattern:
```
<DOCKERHUB_USERNAME>/squash-games-management:<version>
ghcr.io/<github_owner>/squash-games-management:<version>
```

## Key Business Logic Workflows

### Localisation
- **Group messages** (game announcements, scheduled notifications) use the language stored in `bot_groups.language`; fetched via `groupLocalizer(ctx, chatID)` which calls `GET /api/v1/groups/{chatID}`.
- **Private messages** use the language from the Telegram user's `LanguageCode` field via `userLocalizer(langCode)`, falling back to English.
- Three languages are supported: `en` (default), `de`, `ru`. `i18n.Normalize()` maps any Telegram locale string to one of these.
- Scheduler notifications (`RunCancellationReminders`, `RunDayAfterCleanup`) use `groupRepo.GetByID()` directly to resolve group language. Booking reminders use `userLocalizer` with the admin's Telegram `LanguageCode`.
- Date formatting is locale-aware: English "Sunday, March 22", German "Sonntag, 22. März", Russian "Воскресенье, 22 марта".

### Language & Timezone Selection (`/language`)
1. User runs `/language` in private chat.
2. If admin in exactly one group → show language picker inline keyboard for that group immediately.
3. If admin in multiple groups → show group picker first (`set_lang_group:<groupID>` callbacks), then language picker.
4. Language picker shows 3 language buttons + a "🕐 Set Timezone" button.
5. Language buttons send `set_lang:<lang>:<groupID>` callback → `PATCH /api/v1/groups/{chatID}/language` → `bot_groups.language` updated.
6. "Set Timezone" button sends `set_tz_pick:<groupID>` → shows curated timezone picker (18 IANA timezones, 2 per row).
7. Timezone button sends `set_tz:<groupID>:<tz>` → `PATCH /api/v1/groups/{chatID}/timezone` → `bot_groups.timezone` updated.
8. `PATCH /timezone` returns 400 for invalid IANA timezone strings, 404 if group not found.

### Venue Management (`/venues`)
Works in **private chat only**.

1. Admin sends `/venues` → shows venue list for their group (or group picker if multiple groups).
2. Each venue row shows "Edit" and "Delete" buttons; "Add Venue" button at the bottom.
3. **Add venue wizard**: name → courts (comma-separated) → time slots (comma-separated HH:MM, `-` to skip) → address (optional, `-` to skip) → game days (toggle inline keyboard, `-` to skip) → grace period hours (integer or `-` for default 24) → venue created.
4. **Edit venue**: clicking a venue opens an edit menu with buttons for each field (Name, Courts, Time Slots, Address, Game Days, Grace Period). Admin sends new value as free text, except for Game Days which uses the toggle keyboard.
5. **Delete venue**: two-step confirmation; linked games retain their `venue_id` as NULL (ON DELETE SET NULL).

**Venue fields:**
- `grace_period_hours`: hours before game when cancellation reminder fires (default 24). Reminder time = `game_date - (grace_period_hours + 6) hours`.
- `game_days`: comma-separated weekday ints (Go `time.Weekday`: Sunday=0, Monday=1, …, Saturday=6). Used for booking reminder schedule.
- `booking_opens_days`: how many days ahead booking opens (default 14). Included in booking reminder DM text.

Callbacks: `venue_list:{groupID}`, `venue_add:{groupID}`, `venue_edit:{venueID}`, `venue_edit_name/courts/slots/addr/gamedays/graceperiod:{venueID}:{groupID}`, `venue_delete:{venueID}:{groupID}`, `venue_delete_ok:{venueID}:{groupID}`, `venue_day_toggle:{dayNum}`, `venue_day_confirm:_`.
State: `pendingVenueWizard sync.Map` (chatID → `*venueWizard`), `pendingVenueEdit sync.Map` (chatID → `*venueEditState`), `pendingVenueGameDaysEdit sync.Map` (chatID → `*venueGameDaysEditState`).

### New Game Wizard (`/newGame`)
Works in **private chat only**. Group @mentions are redirected to private chat.

At least one venue must be configured per group before creating a game. `/newGame` fails immediately (or at group-selection time for multi-group admins) if no venues exist.

**Single-group admin:**
1. Admin sends `/newGame` → checked for venues immediately; date-picker keyboard shown (today + next 13 days).
2. Admin taps a date → callback `ng_date:<YYYY-MM-DD>` → if 1 venue: auto-select + court toggle; if 2+: venue picker shown.
3. Admin selects a venue → callback `ng_venue:<venueID>` → court toggle buttons (one per court, ✓ when selected) + Confirm button.
4. Admin toggles courts (`ng_court_toggle:<court>`) and confirms (`ng_court_confirm:_`) → time slot buttons (one per slot + "Custom time").
5. Admin selects a slot (`ng_timeslot:<HH:MM>`) or "Custom time" (`ng_time_custom:_`, reverts to free-text) → game created.

**Multi-group admin:**
1. Admin sends `/newGame` → date-picker keyboard.
2. Admin taps a date → callback `ng_date:<YYYY-MM-DD>` → group picker shown (`ng_group:<groupID>` buttons).
3. Admin selects a group → callback `ng_group:<groupID>` → venues fetched for that group; if 0: error (fail); if 1: auto-select + court toggle; if 2+: venue picker.
4. From here, same as single-group admin steps 3–5.
5. Sending any slash command at any step cancels the wizard (`pendingNewGameWizard.Delete`).

State: `pendingNewGameWizard sync.Map` keyed by private `chatID int64`, value `*newGameWizard`.
Wizard steps: `wizardStepGroup` (group picker, multi-group only), `wizardStepVenue` (venue picker), `wizardStepCourtPick` (court toggle), `wizardStepTime` (time input), `wizardStepCourts` (courts free text).
New game callbacks: `ng_date`, `ng_group`, `ng_venue`, `ng_court_toggle`, `ng_court_confirm`, `ng_timeslot`, `ng_time_custom`.

### Courts Update (`/games` → Manage → Edit Courts)
When an admin taps "Edit Courts" in the `/games` manage screen:
- If the game has a linked venue with courts configured → an inline court-toggle keyboard is shown (same ✓ toggle UX as new game creation). Pre-selects the courts already set on the game. Confirming calls `manage_court_confirm:<gameID>`.
- If the game has no venue → falls back to free-text input as before.

Callbacks: `manage_court_toggle:<court>` (toggle), `manage_court_confirm:<gameID>` (confirm and save).
State: `pendingManageCourtsToggle sync.Map` (chatID → `*manageCourtsToggleState`). Cleared on any slash command.

### Button Click Flow
1. Parse callback data (`action:game_id`, e.g. `join:123`, `skip:123`)
2. Update `game_participations` table (upsert player, add/remove guest)
3. Query current participants and guests for the game
4. Format message with updated participant list + "Last updated" footer (using group language via `groupLocalizer`)
5. Edit Telegram message in place (preserving buttons and pin status)
6. Log action at INFO level

### Scheduled Tasks (Cron-based)
A single 5-minute poll cron (`CRON_POLL`, default `*/5 * * * *`) calls `RunScheduledTasks()` which dispatches to three methods:

- **`RunCancellationReminders()`**: Loads all upcoming unnotified games. For each game, computes `reminderAt = game_date - (gracePeriodHours + 6) * hour`. If `|now - reminderAt| ≤ 2m30s`, checks capacity and notifies the group if over/under. Uses `notified_day_before` flag to prevent duplicates. `gracePeriodHours` defaults to 24 if venue has none configured.

- **`RunBookingReminders()`**: Iterates all groups. For each group, checks if local time (using `bot_groups.timezone`) is in the `[10:00, 10:05)` window. For each venue of that group with `game_days` configured, checks if today's weekday is in `game_days`. Deduplicates via `venues.last_booking_reminder_at` (date-scoped). If all conditions met, DMs all group admins with the venue name and `booking_opens_days`.

- **`RunDayAfterCleanup()`**: Iterates all groups. For each group, checks if local time is in the `[03:00, 03:05)` window. Fetches yesterday's uncompleted games for that group. Unpins message, removes keyboard, marks game complete.

Manual trigger via `/trigger` (service admins only) uses the event names `cancellation_reminder`, `booking_reminder`, `day_after_cleanup`.

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
- Emoji header, game date/time, court list, optional venue line (`📍 Name`), numbered player list, guest list
- Capacity line: `courts_count × 2`
- "Last updated: [timestamp]" footer
- "Game completed ✓" marker for finished games

## Environment Variables

**`squash-games-management`:**
```
DATABASE_URL=                # required
TELEGRAM_BOT_TOKEN=          # required (scheduler sends Telegram messages)
INTERNAL_API_SECRET=         # required; shared bearer token for service-to-service auth
SERVER_PORT=8080             # default 8080
CRON_POLL=*/5 * * * *        # default every 5 minutes
LOG_LEVEL=INFO
TIMEZONE=UTC
```

**`telegram-squash-bot`:**
```
TELEGRAM_BOT_TOKEN=          # required
MANAGEMENT_SERVICE_URL=      # required (e.g. http://squash-games-management:8080)
INTERNAL_API_SECRET=         # required; shared bearer token for service-to-service auth
LOG_LEVEL=INFO
TIMEZONE=UTC
SERVICE_ADMIN_IDS=           # optional; comma-separated Telegram user IDs for /trigger
```

**`sports-booking-service`:**
```
EVERSPORTS_EMAIL=            # required; Eversports account email
EVERSPORTS_PASSWORD=         # required; Eversports account password
INTERNAL_API_SECRET=         # required; bearer token for authenticating callers
SERVER_PORT=8081             # default 8081
EVERSPORTS_FACILITY_ID=      # optional; numeric facility ID required for /games and /courts endpoints
EVERSPORTS_COURT_IDS=        # optional; comma-separated numeric court IDs required for /games endpoint
EVERSPORTS_FACILITY_UUID=    # optional; UUID of the facility required for /matches booking creation (default: 6266968c-b0fd-4115-ad3b-ae225cc880f1)
EVERSPORTS_SPORT_UUID=       # optional; UUID of the sport for booking creation and /courts endpoint (default: squash UUID b388b6e6-69de-11e8-bdc6-02bd505aa7b2)
EVERSPORTS_FACILITY_SLUG=    # optional; facility slug from venue URL (e.g. "squash-house-berlin-03"); required for /courts endpoint
EVERSPORTS_SPORT_ID=         # optional; numeric sport ID from booking calendar (e.g. "496" for squash); required for /courts endpoint
EVERSPORTS_SPORT_SLUG=squash # default; sport slug used in /courts request
EVERSPORTS_SPORT_NAME=Squash # default; sport display name used in /courts request
LOG_LEVEL=INFO
TIMEZONE=UTC
```

## Sports Booking Service

A standalone HTTP service (port 8081) that wraps the reverse-engineered Eversports.de internal API. All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.

### Eversports API (reverse-engineered)

**Login** — `POST https://www.eversports.de/api/checkout`
- GraphQL mutation `LoginCredentialLogin` with `credentials: {email, password}` and `params: {origin, region, ...}`
- Session established via the `et` cookie (UUID, 30 days, httpOnly, secure, SameSite=None) set in the response
- Response body may be empty; cookie presence in the jar is the success signal

**Self** — `GET https://www.eversports.de/u/self`
- Called lazily on the first `GetBookings` request (not during login) to retrieve the authenticated user's legacy numeric ID
- Response: `{"status":"success","data":{"user":{"id":<number>,...}}}`
- The `id` field is stored in the client and used by the bookings list endpoint

**Bookings list** — `GET https://www.eversports.de/api/user/activities?userId=<numericID>&past=false`
- Response: `{"status":"success","html":"<ul class=\"past-activities\"><li>...</li></ul>"}`
- HTML fragment parsed with regex; each `<li>` contains:
  - `data-match-relative-link="/match/<uuid>"` — match UUID (absent on some booking types)
  - `<input id="google-calendar-start" value="YYYYMMDDTHHmmss">` / `google-calendar-end` — local venue time, no TZ
  - `<input id="booking-sport" value="Squash">` / `facility-name`
  - `<span class="session-info-value">Court 9</span>` — court name
- `userId` is a **legacy numeric ID** fetched lazily from `GET /u/self` on the first `GetBookings` call

**Single match** — `POST https://www.eversports.de/api/checkout` (GraphQL `Match` query by UUID)
- Returns structured data: id, start/end (RFC3339), state, sport, venue, court, price

**Client API** (`internal/eversports`):
- `New(email, password string, logger) *Client`
- `Login(ctx) error` — stores `et` cookie (called automatically by the public methods)
- `EnsureLoggedIn(ctx) error` — logs in if no session is held; safe for concurrent use
- `GetBookings(ctx) ([]Booking, error)` — auto-logins if needed, calls activities endpoint, parses HTML; retries once on HTTP 401
- `GetMatchByID(ctx, matchID string) (*Booking, error)` — auto-logins if needed, single match via GraphQL; retries once on HTTP 401
- `GetSlots(ctx, facilityID, courtIDs, startDate) ([]Slot, error)` — auto-logins if needed; retries once on HTTP 401
- `GetCourts(ctx, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]Court, error)` — form-encodes POST to `/api/booking/calendar/update` for the given YYYY-MM-DD date, parses `<tr class="court">` rows; deduplicates by numeric court ID; retries once on HTTP 401

### HTTP endpoints

All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.
Authentication with Eversports is handled automatically: the service logs in on the first request and re-authenticates if the session expires.

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/health` | Liveness probe (no auth) |
| `GET`  | `/version` | Service version (no auth) |
| `GET`  | `/api/v1/eversports/matches` | List upcoming bookings |
| `POST` | `/api/v1/eversports/matches` | Create a booking; body `{courtUuid, start, end}` (RFC 3339); returns `{bookingUuid, bookingId}`. Requires `EVERSPORTS_FACILITY_UUID` + `EVERSPORTS_SPORT_UUID` |
| `GET`  | `/api/v1/eversports/matches/{id}` | Fetch single booking by UUID |
| `DELETE` | `/api/v1/eversports/matches/{id}` | Cancel a booking by UUID; returns `{id, state, relativeLink}` |
| `GET`  | `/api/v1/eversports/games?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Court reservations for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means reserved. Optional `startTime`/`endTime` filter to a time window (inclusive); optional `my` filters by user ownership (`isUserBookingOwner`). Requires `EVERSPORTS_FACILITY_ID` + `EVERSPORTS_COURT_IDS` |
| `GET`  | `/api/v1/eversports/courts` | List courts at the facility; returns `[{id, uuid, name}]`. Parses `POST /api/booking/calendar/update` HTML. Requires `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_SLUG`, `EVERSPORTS_SPORT_ID`, `EVERSPORTS_SPORT_UUID` |
| `GET`  | `/api/v1/eversports/facility` | Venue profile for the configured facility slug; returns `{id, slug, name, rating, reviewCount, address, hideAddress, tags, contact, sports, city, company}`. Requires `EVERSPORTS_FACILITY_SLUG` |

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

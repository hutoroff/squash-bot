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
telegram  →  HTTP API  →  management  →  PostgreSQL
booking   →  eversports.de  (reverse-engineered cookie-auth API)
```

Four independent binaries in one Go module (`github.com/vkhutorov/squash_bot`):

**`management`** (`cmd/management/`)
- **api/**: HTTP handlers (REST JSON API on port 8080)
- **service/**: Business logic layer; defines `TelegramAPI`, `Notifier`, and repository interfaces (`GameRepository`, `PlayerRepository`, `ParticipationRepository`, `GuestRepository`, `GroupRepository`, `VenueRepository`) in `interfaces.go`; four focused scheduled-job structs (`CancellationReminderJob`, `BookingReminderJob`, `DayAfterCleanupJob`, `AutoBookingJob`) orchestrated by a thin `Scheduler`; shared timezone/language helpers in `group_resolver.go`; `ParticipationService` fires async Telegram message edits via an injected `Notifier`
- **storage/**: SQL repository implementations satisfying the interfaces defined in `service/`
- Runs the cron scheduler; sends Telegram messages via `TelegramAPI` interface (wired to `*tgbotapi.BotAPI` in `main.go`)

**`telegram`** (`cmd/telegram/`)
- **telegram/**: Bot loop, callback handlers, slash commands, message formatting
- **client/**: Typed HTTP client that calls the management service API
- No DB access; all data operations go through HTTP

**`booking`** (`cmd/booking/`)
- **eversports/**: Reverse-engineered Eversports HTTP client (login, bookings list, single match)
- **booking/**: REST API wrapping the Eversports client (port 8081)
- No DB access; stateless except for the in-memory cookie jar that holds the session

**`web`** (`cmd/web/`)
- **webserver/**: HTTP server (port 8082), SPA static-file handler, Telegram Login Widget auth, JWT session management, web API endpoints for games and participation
- **web/frontend/**: React + TypeScript SPA (Vite); compiled output embedded in the Go binary via `web/embed.go`
- No DB access; authenticates users via Telegram Login Widget, calls the management service for data
- Participation actions (join/skip/+1/-1) proxy through to the management service and trigger a live Telegram message edit via `GameNotifier` (`cmd/management/service`)

**Shared**
- **models/**: Game, Player, GameParticipation, GuestParticipation, Group (all with JSON tags); `PlayerGame` — read-only aggregated view (game + participation status + participant count + group timezone) returned by `GET /api/v1/players/{id}/games`
- **i18n/**: `Lang` type (`en`/`de`/`ru`), `Normalize(code)` maps Telegram `LanguageCode` to a supported lang, `Localizer` provides `T(key)`, `Tf(key, args...)`, `FormatGameDate(t)`, `FormatUpdatedAt(t)`, `FormatDayMonth(t)`, `ShortWeekday(w)`
- **gameformat/**: Shared game message formatter and keyboard builder used by both the telegram bot and the management service. `FormatGameMessage(game, participations, guests, loc, now, lz)` produces the announcement text; `GameKeyboard(gameID, lz)` builds the inline keyboard; `PlayerDisplayName(p)` formats a player's display name.

### Database Schema
- `games`: id, chat_id, message_id, game_date, courts_count, courts, venue_id (nullable FK→venues), notified_day_before, completed, created_at
- `players`: id, telegram_id (UNIQUE), username, first_name, last_name, created_at
- `game_participations`: id, game_id, player_id, status ('registered'|'skipped'), created_at, UNIQUE(game_id, player_id)
- `guest_participations`: id, game_id, invited_by_player_id, created_at
- `bot_groups`: chat_id PK, title, bot_is_admin, language (VARCHAR(5) DEFAULT 'en'), timezone (VARCHAR(64) DEFAULT 'UTC'), added_at
- `venues`: id, group_id (FK→bot_groups), name, courts (comma-separated), time_slots (comma-separated HH:MM), address (nullable), grace_period_hours (INT DEFAULT 24), game_days (TEXT DEFAULT ''), booking_opens_days (INT DEFAULT 14), last_booking_reminder_at (TIMESTAMPTZ nullable), preferred_game_time (TEXT DEFAULT ''), last_auto_booking_at (TIMESTAMPTZ nullable), auto_booking_courts (TEXT DEFAULT ''), auto_booking_enabled (BOOLEAN DEFAULT FALSE), created_at, UNIQUE(group_id, name)

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
  go run cmd/management/main.go

# Run telegram bot locally
MANAGEMENT_SERVICE_URL=http://localhost:8080 \
  TELEGRAM_BOT_TOKEN=<token> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/telegram/main.go

# Run booking service locally
EVERSPORTS_EMAIL=<email> \
  EVERSPORTS_PASSWORD=<password> \
  INTERNAL_API_SECRET=<secret> \
  go run cmd/booking/main.go

# Testing
go test ./...
go test -tags integration -timeout 120s ./...

# Build all binaries
go build ./cmd/management/
go build ./cmd/telegram/
go build ./cmd/booking/

# Build with explicit version (mirrors what Docker does)
go build -ldflags="-X main.Version=1.2.3" ./cmd/management/
go build -ldflags="-X main.Version=1.2.3" ./cmd/telegram/
go build -ldflags="-X main.Version=1.2.3" ./cmd/booking/
```

## Versioning & Release

Each service has its own independent version stored in a plain-text file:
- `cmd/management/VERSION`
- `cmd/telegram/VERSION`
- `cmd/booking/VERSION`
- `cmd/web/VERSION`

Format: `MAJOR.MINOR.BUILD` (e.g. `1.0.33`).

Version is injected at build time via `-ldflags "-X main.Version=<ver>"` and logged on startup. The management service exposes `GET /version` (unauthenticated, like `/health`) returning `{"version": "1.0.33"}`.

**API compatibility rule**: services are compatible within the same major version. The telegram bot calls `GET /version` on the management service at startup and exits with an error if the major versions differ.

### Releasing a service

Trigger the relevant GitHub Actions workflow manually:
- **Actions → Release Management Service** for `management`
- **Actions → Release Telegram Bot** for `telegram`
- **Actions → Release Booking Service** for `booking`
- **Actions → Release Web Service** for `web`

Select bump type (`patch` / `minor` / `major`). The workflow will:
1. Verify CI passed for the exact HEAD commit (fails immediately otherwise). The web release additionally verifies the `frontend-test` job alongside `build-and-test`.
2. Bump the `VERSION` file.
3. Build and push Docker images tagged `<version>` and `latest` to both Docker Hub and GHCR.
4. Commit the bumped `VERSION` back to the branch and create a git tag (`management/vX.Y.Z`, `telegram/vX.Y.Z`, `booking/vX.Y.Z`, or `web/vX.Y.Z`).

**Required GitHub configuration (one-time setup):**
- Variable `DOCKERHUB_USERNAME` — Docker Hub org or username for image names.
- Secret `DOCKERHUB_TOKEN` — Docker Hub access token with push rights.
- Secret `RELEASE_PAT` — a Personal Access Token (classic: `repo` scope; fine-grained: `Contents: Read and Write`) whose owner is listed as a bypass actor in the branch-protection rule for `main`. Required so the "Commit and tag" step can push the VERSION bump directly to a protected branch. `GITHUB_TOKEN` cannot bypass branch protection.
- `GITHUB_TOKEN` is used automatically for GHCR pushes and the check-runs API.

Image names follow the pattern (service → image name):
```
management  →  <DOCKERHUB_USERNAME>/squash-management:<version>
telegram    →  <DOCKERHUB_USERNAME>/squash-telegram-bot:<version>
booking     →  <DOCKERHUB_USERNAME>/squash-booking-eversports:<version>
web         →  <DOCKERHUB_USERNAME>/squash-web:<version>
```

## Key Business Logic Workflows

### Localisation
- **Group messages** (game announcements, scheduled notifications) use the language stored in `bot_groups.language`; fetched via `groupLocalizer(ctx, chatID)` which calls `GET /api/v1/groups/{chatID}`.
- **Private messages** use the language from the Telegram user's `LanguageCode` field via `userLocalizer(langCode)`, falling back to English.
- Three languages are supported: `en` (default), `de`, `ru`. `i18n.Normalize()` maps any Telegram locale string to one of these.
- Scheduler notifications (`CancellationReminderJob`, `DayAfterCleanupJob`) use `groupRepo.GetByID()` directly to resolve group language via the `groupLang` helper in `group_resolver.go`. Booking reminders use `userLocalizer` with the admin's Telegram `LanguageCode`.
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
3. **Add venue wizard**: name → courts (comma-separated) → time slots (comma-separated HH:MM, `-` to skip) → preferred game time (inline buttons from time_slots, skipped if no time_slots entered) → address (optional, `-` to skip) → game days (toggle inline keyboard; tap days to toggle, press "✓ Confirm" to proceed — confirming with nothing selected skips the field) → grace period hours (integer or `-` for default 24) → **auto-booking enabled** (inline "Enable"/"Disable" buttons; default disabled) → auto-booking courts (ordered subset or `-`; only shown when auto-booking was enabled) → booking opens days (integer or `-` for default 14) → venue created.
4. **Edit venue**: clicking a venue opens an edit menu showing current values and buttons for each field. Free-text fields (Name, Courts, Time Slots, Address, Grace Period, Auto-booking Courts, Booking Opens): admin sends new value as a message. Inline-keyboard fields: Game Days uses the toggle keyboard (tap days + Confirm); Preferred Time shows buttons for each configured time slot plus a "✕ No preference" option.
5. **Delete venue**: two-step confirmation; linked games retain their `venue_id` as NULL (ON DELETE SET NULL).

**Venue fields:**
- `grace_period_hours`: hours before game when cancellation reminder fires (default 24). Reminder time = `game_date - (grace_period_hours + 6) hours`.
- `game_days`: comma-separated weekday ints (Go `time.Weekday`: Sunday=0, Monday=1, …, Saturday=6). Used for booking reminder schedule.
- `booking_opens_days`: how many days ahead booking opens (default 14). Included in booking reminder DM text.
- `preferred_game_time`: a single HH:MM slot (must be one of `time_slots`, or empty for no preference). Displayed with ⭐ in the new-game wizard time slot keyboard. Set via `venue_edit_preferred_time` button or the venue creation wizard.
- `auto_booking_enabled`: whether automatic court booking is enabled for this venue (default false). `RunAutoBooking` skips venues where this is false. Toggled via `venue_toggle_autobooking:<venueID>:<groupID>` callback or set during venue creation wizard.
- `auto_booking_courts`: ordered comma-separated court IDs (subset of `courts`) that `RunAutoBooking` will attempt to book, in declared priority order. Empty means any available court is eligible (API response order). Validated: each ID must be present in `courts`. Only editable (via `venue_edit_auto_booking_courts` button) when `auto_booking_enabled = true`. Set in venue creation wizard only if auto-booking was enabled at the enable/disable step.

Callbacks: `venue_list:{groupID}`, `venue_add:{groupID}`, `venue_edit:{venueID}`, `venue_edit_name/courts/slots/addr/gamedays/graceperiod/preferred_time/auto_booking_courts/booking_opens_days:{venueID}:{groupID}`, `venue_delete:{venueID}:{groupID}`, `venue_delete_ok:{venueID}:{groupID}`, `venue_day_toggle:{dayNum}`, `venue_day_confirm:_`, `venue_wiz_ptime:{slot|_skip}`, `venue_ptime_set:{venueID}:{slot|_clear}`, `venue_wiz_autobooking:{enable|disable}`, `venue_toggle_autobooking:{venueID}:{groupID}`.
State: `pendingVenueWizard sync.Map` (chatID → `*venueWizard`), `pendingVenueEdit sync.Map` (chatID → `*venueEditState`), `pendingVenueGameDaysEdit sync.Map` (chatID → `*venueGameDaysEditState`), `pendingVenuePreferredTimeEdit sync.Map` (chatID → `*venuePreferredTimeEditState`).

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
A single 5-minute poll cron (`CRON_POLL`, default `*/5 * * * *`) calls `Scheduler.RunScheduledTasks()` which calls `run(false)` on each registered job. `Scheduler.ForceRun(event)` calls `run(true)` on the named job, bypassing time-window gates. Each job is an independent struct implementing the unexported `scheduledJob` interface (`run(force bool)` + `name() string`).

- **`CancellationReminderJob`** (`cancellation_reminder.go`): Loads all upcoming unnotified games. For each game, computes `reminderAt = game_date - (gracePeriodHours + 6) * hour`. If `|now - reminderAt| ≤ 2m30s`, checks capacity, attempts automatic court cancellation (if `SPORTS_BOOKING_SERVICE_URL` is configured), and **always** sends a group notification. Uses `notified_day_before` flag to prevent duplicates. `gracePeriodHours` defaults to 24 if venue has none configured.

  **Court cancellation flow** (when booking service URL is configured):
  1. Computes `courtsToCancel = floor((capacity − count) / 2)` — fully unused courts (each needs 2 free spots).
  2. Calls `GET /api/v1/eversports/matches?date=…&startTime=…&endTime=startTime+10m&my=true` to get own bookings.
  3. Selects courts to cancel in two phases:
     - **Phase 1 — priority order** (when the game's venue has `auto_booking_courts` configured): iterate `auto_booking_courts` in **reverse** (lowest-priority first) and pick courts that are actually booked, up to `courtsToCancel`.
     - **Phase 2 — consecutive-grouping fallback**: if fewer than `courtsToCancel` courts were selected in phase 1 (including when `auto_booking_courts` is empty or all priority courts are already unbooked), apply the grouping algorithm to the remaining booked courts: split by consecutive-run, pick from the smallest group (tie-break by lowest first court), cancel from the end of that group. This handles courts booked outside the priority list (e.g. manually added).
  4. Cancels selected courts one-by-one via `DELETE /api/v1/eversports/matches/{matchUUID}`. Partial failures do not abort remaining cancellations.
  5. Updates `games.courts` / `games.courts_count` in the DB.

  **Notification scenarios** (always sent, determined after cancellation):
  - `all_good` — count ≥ newCapacity: "upcoming game, courts confirmed".
  - `canceled_balanced` — courts canceled and count now == newCapacity: "courts X canceled, all set".
  - `odd_no_cancel` — odd player count, nothing canceled, 1 free spot: "1 free spot".
  - `odd_canceled` — odd player count, some courts canceled, 1 free spot: "courts X canceled, 1 free spot".
  - `all_canceled` — all courts canceled: "game will not happen".
  - `even_no_cancel` — even player count, count < capacity, nothing (or not enough) canceled: "please cancel unused courts". Fires when the booking service is absent or found no owned bookings.

- **`BookingReminderJob`** (`booking_reminder.go`): Iterates all groups. For each group, checks if local time (using `bot_groups.timezone`) is in the `[10:00, 10:05)` window. For each venue of that group with `game_days` configured, checks if today's weekday is in `game_days`. Deduplicates via `venues.last_booking_reminder_at` (date-scoped). After the dedup check, queries `GetUncompletedGamesByGroupAndDay` for the target date (`today + booking_opens_days`); if a game already exists for that day the reminder is skipped entirely (dedup guard is NOT updated, so the check retries on the next poll). If `venues.last_auto_booking_at` is set for today, sends a group notification instead of DM (confirming auto-booking was done). Otherwise DMs all group admins with the venue name and `booking_opens_days`, using Markdown formatting. The DM message reads: *"Booking is open now! Game in N days — don't forget to reserve courts."*

- **`AutoBookingJob`** (`auto_booking.go`): Iterates all groups. For each group, checks if local time is in the `[00:00, 00:05)` window. For each venue with `auto_booking_enabled = true` and `game_days` and `preferred_game_time` configured, checks if today's weekday is in `game_days`. Deduplicates via `venues.last_auto_booking_at` (date-scoped). If conditions met and `bookingClient` is set:
  1. Fetches available (unbooked) slots at `preferred_game_time` ± 10 min via `ListMatches(my=false)` for the date `today + booking_opens_days`.
  2. Selects courts via `filterAvailableCourts`: if `auto_booking_courts` is set, returns only those IDs in declared order (priority booking); otherwise returns all available venue courts in API response order.
  3. Books up to `AUTO_BOOKING_COURTS_COUNT` courts (1 hour each) via `BookMatch`.
  4. On success: sends group notification; sets `last_auto_booking_at`.
  5. On partial/full failure: immediately DMs all group admins silently (no notification sound).

- **`DayAfterCleanupJob`** (`day_after_cleanup.go`): Iterates all groups. For each group, checks if local time is in the `[03:00, 03:05)` window. Fetches yesterday's uncompleted games for that group. Unpins message, removes keyboard, marks game complete.

Manual trigger via `/trigger` (service admins only) uses the event names `cancellation_reminder`, `booking_reminder`, `day_after_cleanup`, `auto_booking`. Each event routes to `Scheduler.ForceRun(event)` which calls `run(true)` on the matching job, bypassing the time-window scheduling gate (e.g. `[00:00, 00:05)` for auto-booking, `±pollWindow` for cancellation reminders) but still respects `game_days` validation and same-day dedup guards (`last_auto_booking_at`, `last_booking_reminder_at`, `notified_day_before`).

### Admin & Group Management
- **Group admin rights** are verified dynamically per group via `GetChatAdministrators` — no hardcoded IDs; this controls game creation, player/guest management, and all `/games` actions
- **Service admin access** (`SERVICE_ADMIN_IDS` env var) is a separate, operator-configured set of Telegram user IDs that grants access to `/trigger` only; it is independent of group membership or Telegram admin status
- `my_chat_member` events track when the bot is added/removed/promoted/demoted
- If added without admin rights, bot DMs the user who added it

### Guest Management
- Players can add guests (+1) linked to their player record
- Players can remove their own most-recently-added guest
- Admins can remove any guest via the `/games` management menu

### Web Authentication (`web`)
Authentication uses the [Telegram Login Widget](https://core.telegram.org/widgets/login).

**Flow:**
1. `GET /api/config` returns `{"bot_name":"<TELEGRAM_BOT_NAME>"}` — frontend uses this to render the widget.
2. User clicks the widget button and approves in the Telegram app.
3. Telegram redirects the browser to `GET /api/auth/callback?id=…&first_name=…&auth_date=…&hash=…`.
4. Backend verifies the HMAC-SHA256 hash: `key = SHA256(TELEGRAM_BOT_TOKEN)`, `sig = hex(HMAC-SHA256(key, sorted_key=value_pairs))`. Also checks `auth_date` is ≤ 86400 s old.
5. Backend calls `GET /api/v1/players/{telegramID}` on the management service (with `INTERNAL_API_SECRET`). Returns `player_id` if the user has previously used the Telegram bot; `nil` if not (login still succeeds).
6. Backend issues a signed HS256 JWT (`JWT_SECRET`, 7-day expiry) and sets it as an HttpOnly, SameSite=Lax `session` cookie. The `Secure` flag is set when `r.TLS != nil` **or** `X-Forwarded-Proto: https` (covers TLS-terminating proxies).
7. `GET /api/auth/me` — returns `200` + user JSON from the cookie, or `401` if absent/expired/invalid.
8. `POST /api/auth/logout` — expires the cookie.
9. `GET /api/games` — returns a JSON array of the authenticated user's games (see below). Player ID is read from the JWT claim only — never from a query parameter. If `player_id` is absent from the JWT (user logged in before their first bot interaction), a live `lookupPlayer` call is made by `TelegramID`; if a record is now found the session cookie is refreshed in the same response with an updated JWT so subsequent requests skip the re-lookup. Returns `[]` when no player record exists yet; returns `502` on management service errors.

**BotFather setup (one-time per deployment):** `/mybots` → select bot → Bot Settings → Domain → enter the public hostname (no `https://`). `localhost` is not accepted; use a tunnel (e.g. ngrok) for local end-to-end testing.

**Management service endpoints used by the web service:**
- `GET /api/v1/players/{telegramID}` — returns the `players` row for the given Telegram user ID (`200` + JSON) or `404` if not found. Used during login to populate `player_id` in the JWT.
- `GET /api/v1/players/{playerID}/games` — returns all `PlayerGame` records for a player (newest first). Each item: `id`, `game_date`, `courts_count`, `courts`, `completed`, `participation_status` (`registered`|`skipped`), `participant_count` (registered players + guests), `venue_name`, `venue_address`, `group_title`, `timezone`. Used by `GET /api/games`. Both endpoints require bearer auth.

### Web Participation Flow (`web`)

The SPA lets authenticated users manage their participation from the browser. The past-games section is **collapsed by default**; `GameCard` components for past games are not mounted until the user expands the section, which avoids unnecessary `GET /api/games/{id}/participants` calls.

**Web API endpoints (all require the `session` cookie; player ID taken from JWT only):**

| Method   | Path                             | Action                                      |
|----------|----------------------------------|---------------------------------------------|
| `GET`    | `/api/games/{id}/participants`   | Return participants + guests for a game     |
| `POST`   | `/api/games/{id}/join`           | Register the current user for the game      |
| `POST`   | `/api/games/{id}/skip`           | Mark the current user as skipped            |
| `POST`   | `/api/games/{id}/guests`         | Add a guest linked to the current user      |
| `DELETE` | `/api/games/{id}/guests`         | Remove the current user's most-recent guest |

Each mutating action (join/skip/+1/-1) calls the corresponding management service endpoint with the player ID from the JWT. The management service's `ParticipationService` fires `Notifier.EditGameMessage(ctx, gameID)` asynchronously after each mutation to re-fetch participants and edit the Telegram announcement in place. Returns the updated `GameParticipants` payload to the frontend so the UI refreshes without a second round-trip.

**`GameNotifier`** (`cmd/management/service/game_notifier.go`): concrete implementation of the `Notifier` interface. Fetches game + participants + guests, re-formats the message and keyboard via `gameformat`, then calls `EditMessageText` via the `TelegramAPI` interface. Resolves the group timezone via `resolveGroupTimezone` in `group_resolver.go` (falls back to the default location on invalid IANA strings).

### Message Formatting
- Emoji header, game date/time, court list, optional venue line (`📍 Name`), numbered player list, guest list
- Capacity line: `courts_count × 2`
- "Last updated: [timestamp]" footer
- "Game completed ✓" marker for finished games

## Environment Variables

**`management`:**
```
DATABASE_URL=                # required
TELEGRAM_BOT_TOKEN=          # required (scheduler sends Telegram messages)
INTERNAL_API_SECRET=         # required; shared bearer token for service-to-service auth
SERVER_PORT=8080             # default 8080
CRON_POLL=*/5 * * * *        # default every 5 minutes
LOG_LEVEL=INFO
TIMEZONE=UTC
SPORTS_BOOKING_SERVICE_URL=  # optional; e.g. http://booking:8081
                             # when set, enables: cancellation reminder auto-cancels courts,
                             # and RunAutoBooking auto-books courts at midnight when booking opens
AUTO_BOOKING_COURTS_COUNT=3  # optional; courts to auto-book at midnight (default 3)
                             # requires SPORTS_BOOKING_SERVICE_URL
```

**`telegram`:**
```
TELEGRAM_BOT_TOKEN=          # required
MANAGEMENT_SERVICE_URL=      # required (e.g. http://management:8080)
INTERNAL_API_SECRET=         # required; shared bearer token for service-to-service auth
LOG_LEVEL=INFO
TIMEZONE=UTC
SERVICE_ADMIN_IDS=           # optional; comma-separated Telegram user IDs for /trigger
```

**`booking`:**
```
EVERSPORTS_EMAIL=            # required; Eversports account email
EVERSPORTS_PASSWORD=         # required; Eversports account password
INTERNAL_API_SECRET=         # required; bearer token for authenticating callers
SERVER_PORT=8081             # default 8081
EVERSPORTS_FACILITY_ID=      # optional; numeric facility ID required for GET /matches and /courts endpoints
EVERSPORTS_FACILITY_UUID=    # optional; UUID of the facility required for POST /matches booking creation (default: 6266968c-b0fd-4115-ad3b-ae225cc880f1)
EVERSPORTS_FACILITY_SLUG=    # optional; facility slug from venue URL (e.g. "squash-house-berlin-03"); required for GET /matches and /courts
LOG_LEVEL=INFO
TIMEZONE=UTC
```

**`web`:**
```
TELEGRAM_BOT_TOKEN=          # required; verifies Telegram Login Widget HMAC-SHA256 callbacks
TELEGRAM_BOT_NAME=           # required; bot username without @ shown in the Login Widget (e.g. SquashBot)
MANAGEMENT_SERVICE_URL=      # required (e.g. http://management:8080); pre-set in docker-compose.yml
INTERNAL_API_SECRET=         # required; shared bearer token for calling the management service
JWT_SECRET=                  # required; signs/verifies session cookies (HS256, 7-day expiry); generate with openssl rand -hex 32
SERVER_PORT=8082             # default 8082
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

**Single match** — `POST https://www.eversports.de/api/checkout` (GraphQL `Match` query by UUID)
- Returns structured data: id, start/end (RFC3339), state, sport, venue, court, price

**Client API** (`cmd/booking/eversports`):
- `New(email, password string, logger) *Client`
- `Login(ctx) error` — stores `et` cookie (called automatically by the public methods)
- `EnsureLoggedIn(ctx) error` — logs in if no session is held; safe for concurrent use
- `GetMatchByID(ctx, matchID string) (*Booking, error)` — auto-logins if needed, single match via GraphQL; retries once on HTTP 401
- `GetSlots(ctx, facilityID, courtIDs, startDate) ([]Slot, error)` — auto-logins if needed; retries once on HTTP 401
- `GetCourts(ctx, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]Court, error)` — form-encodes POST to `/api/booking/calendar/update` for the given YYYY-MM-DD date, parses `<tr class="court">` rows; deduplicates by numeric court ID; retries once on HTTP 401
- `CreateBooking(ctx, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*BookingResult, error)` — 5-step checkout flow: reserve court → pay-offline → create-from-booking → getMPFeeForCourtBooking → trackCheckoutCompleted; returns `BookingResult{BookingUUID, BookingID, MatchID}` (`MatchID` is best-effort, empty on failure); serialised access via internal mutex
- `CancelMatch(ctx, matchID string) (*CancellationResult, error)` — cancels a booking via GraphQL `CancelMatch` mutation; retries once on HTTP 401

### HTTP endpoints

All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.
Authentication with Eversports is handled automatically: the service logs in on the first request and re-authenticates if the session expires.

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/health` | Liveness probe (no auth) |
| `GET`  | `/version` | Service version (no auth) |
| `GET`  | `/api/v1/eversports/matches?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Court reservations for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means reserved. Optional `startTime`/`endTime` filter to a time window (inclusive); optional `my` filters by user ownership (`isUserBookingOwner`). Requires `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_SLUG` (courts are resolved dynamically) |
| `POST` | `/api/v1/eversports/matches` | Create a booking; body `{courtUuid, start, end}` (RFC 3339); returns `{bookingUuid, bookingId, matchId}` (`matchId` omitted if match creation failed). Requires `EVERSPORTS_FACILITY_UUID` |
| `GET`  | `/api/v1/eversports/matches/{id}` | Fetch single booking by match UUID (the `matchId` returned by `POST /matches`) |
| `DELETE` | `/api/v1/eversports/matches/{id}` | Cancel a booking by match UUID (`matchId` from `POST /matches`, **not** `bookingUuid`); returns `{id, state, relativeLink}` |
| `GET`  | `/api/v1/eversports/courts[?date=YYYY-MM-DD]` | List courts at the facility; returns `[{id, uuid, name}]`. Parses `POST /api/booking/calendar/update` HTML. Optional `date` parameter (default: today). Requires `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_SLUG` |
| `GET`  | `/api/v1/eversports/facility?slug=<slug>` | Venue profile for a facility slug (e.g. `squash-house-berlin-03`); returns `{id, slug, name, rating, reviewCount, address, hideAddress, tags, contact, sports, city, company}`. `slug` query parameter is mandatory (400 if missing). |

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

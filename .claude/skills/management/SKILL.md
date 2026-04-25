---
name: management
description: Architecture reference for the squash-bot management service (cmd/management). Load before planning any changes to API handlers, business logic, scheduler jobs, or storage layer.
user-invocable: true
---

# Management Service — Architecture Reference

The management service is the central data hub. It owns the PostgreSQL database, all business logic, and the cron scheduler. The telegram bot and web service call it over HTTP; it never calls them back (the `Notifier` interface is injected at startup via `TelegramAPI`).

**Entry point:** `cmd/management/main.go`  
**Port:** 8080 (env `SERVER_PORT`)  
**Module path:** `github.com/hutoroff/squash-bot/cmd/management`

---

## Package structure

```
cmd/management/
├── main.go            — wiring: DB pool, repos, services, HTTP server, cron
├── api/
│   ├── server.go      — Handler struct, RegisterRoutes, NewServer, requireBearer middleware
│   ├── games.go       — game + participation HTTP handlers
│   ├── groups.go      — group HTTP handlers
│   ├── venues.go      — venue HTTP handlers
│   └── scheduler.go   — POST /api/v1/scheduler/trigger/{event}
└── service/
│   ├── interfaces.go           — ALL repository + Telegram interfaces (source of truth)
│   ├── game_service.go         — GameService: Create, GetByID, UpdateCourts, …
│   ├── participation_service.go — ParticipationService: Join, Skip, AddGuest, RemoveGuest, KickPlayer, KickGuest
│   ├── venue_service.go        — VenueService: Create, GetByGroup, Update, Delete
│   ├── game_notifier.go        — GameNotifier (implements Notifier): EditGameMessage
│   ├── scheduler.go            — Scheduler: RunScheduledTasks, ForceRun, registers 4 jobs
│   ├── cancellation_reminder.go — CancellationReminderJob
│   ├── booking_reminder.go     — BookingReminderJob
│   ├── auto_booking.go         — AutoBookingJob
│   ├── day_after_cleanup.go    — DayAfterCleanupJob
│   ├── court_cancellation.go   — court cancellation logic (used by CancellationReminderJob)
│   ├── booking_client.go       — BookingServiceClient interface + HTTP client
│   └── group_resolver.go       — resolveGroupTimezone, groupLang helpers
└── storage/
    ├── postgres.go              — pool setup, migration runner
    ├── game_repo.go             — GameRepo implements GameRepository
    ├── player_repo.go           — PlayerRepo implements PlayerRepository
    ├── participation_repo.go    — ParticipationRepo implements ParticipationRepository
    ├── guest_repo.go            — GuestRepo implements GuestRepository
    ├── group_repo.go            — GroupRepo implements GroupRepository
    ├── venue_repo.go            — VenueRepo implements VenueRepository
    ├── venue_credential_repo.go — VenueCredentialRepo implements VenueCredentialRepository
    └── court_booking_repo.go    — CourtBookingRepo implements CourtBookingRepository
```

---

## Key interfaces (`service/interfaces.go`)

All interfaces are defined here and implemented in `storage/`. Never import `storage` from `service` — dependency flows one way: `api` → `service` ← `storage` (injected via main.go).

```
TelegramAPI      — Send, Request, GetChatAdministrators (satisfied by *tgbotapi.BotAPI)
Notifier         — EditGameMessage(ctx, gameID int64)
GameRepository   — Create, GetByID, GetUpcomingGames, UpdateMessageID, UpdateCourts,
                   GetNextGameForTelegramUser, GetGamesForPlayer, GetUpcomingUnnotifiedGames,
                   GetUncompletedGamesByGroupAndDay, MarkNotifiedDayBefore, MarkCompleted
PlayerRepository — Upsert, GetByTelegramID
ParticipationRepository — Upsert, GetByGame, DeleteByGameAndPlayer, GetRegisteredCount
GuestRepository  — AddGuest, RemoveLatestGuest, GetByGame, DeleteByID, GetCountByGame
GroupRepository  — Upsert, SetLanguage, SetTimezone, Remove, Exists, GetByID, GetAll
VenueRepository  — Create, GetByID, GetByIDAndGroupID, GetByGroupID, Update, Delete,
                   SetLastBookingReminderAt, SetLastAutoBookingAt
VenueCredentialRepository — Create(venueID, login, encPassword, priority, maxCourts),
                   ListByVenueID, ListWithPasswordByVenueID, GetWithPasswordByID(id),
                   Delete(id, venueID), ExistsByLogin(venueID, login),
                   PrioritiesInUse(venueID), SetLastErrorAt(id)
CourtBookingRepository — Save(ctx, *models.CourtBooking),
                   GetByVenueAndDate(ctx, venueID, gameDate) — returns only active (canceled_at IS NULL),
                   GetByVenueAndDateAndTime(ctx, venueID int64, date time.Time, gameTime string) — active rows filtered by game_time column,
                   MarkCanceled(ctx, matchID) — soft-delete: sets canceled_at = NOW(),
                   MarkCanceledByVenueAndDate(ctx, venueID, gameDate) — bulk soft-delete all active rows for venue+date,
                   HasActiveByCredentialID(ctx, credentialID) bool,
                   HasActiveByVenueID(ctx, venueID) bool
AutoBookingResultRepository — Save(ctx, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int),
                   GetByVenueAndDate(ctx, venueID int64, date time.Time) []*models.AutoBookingResult,
                   GetByVenueAndDateAndTime(ctx, venueID int64, date time.Time, gameTime string) *models.AutoBookingResult — nil if not yet booked,
                   GetByGameID(ctx, gameID int64) *models.AutoBookingResult — used by cancellation to find game_time,
                   SetGameID(ctx, resultID, gameID int64) — links result to the Telegram game created by BookingReminderJob
```

`VenueCredentialService` (`service/venue_credential_service.go`) wraps the repository with:
- `Add(ctx, venueID, groupID, login, password, priority, maxCourts)` — validates venue ownership, deduplicates by login, encrypts password via `Encryptor`, stores via repo
- `List(ctx, venueID, groupID)` — validates ownership, returns credentials without passwords
- `Remove(ctx, credID, venueID, groupID)` — validates ownership; returns `ErrCredentialInUse` if `courtBookingRepo.HasActiveByCredentialID` is true; returns `ErrCredentialNotFound` if venue ownership check or `repo.Delete` fails; deletes
- `GetDecryptedByID(ctx, credID)` — fetches single credential by ID, decrypts password; used by `CancellationReminderJob` at cancel time
- `PrioritiesInUse(ctx, venueID, groupID)` — returns sorted priority list for wizard UI
- `ListForBooking(ctx, venueID, cooldown)` — returns `[]DecryptedCredential` ordered by priority, excluding credentials where `last_error_at > NOW() - cooldown`; decrypts passwords
- `MarkError(ctx, credID)` — sets `last_error_at = NOW()` via `SetLastErrorAt`

`DecryptedCredential` (internal, never serialised): `ID, VenueID int64`, `Login, Password string`, `Priority, MaxCourts int`.

`VenueService.DeleteVenue` returns `ErrVenueHasActiveBookings` (→ HTTP 409) if `courtBookingRepo.HasActiveByVenueID` is true.

**Rule:** If a new operation is needed, add a method to the correct interface first, then implement it in the storage package, then use it in the service. Do not bypass interfaces.

---

## HTTP API routes (`api/server.go → RegisterRoutes`)

All routes except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.
Auth is enforced by `requireBearer` middleware (constant-time comparison).

```
GET  /health
GET  /version

POST   /api/v1/games                               — createGame
GET    /api/v1/games                               — listGames (query: chatIDs)
GET    /api/v1/games/{id}                          — getGame
PATCH  /api/v1/games/{id}/message-id               — updateMessageID
PATCH  /api/v1/games/{id}/courts                   — updateCourts

POST   /api/v1/games/{id}/join                     — joinGame
POST   /api/v1/games/{id}/skip                     — skipGame
POST   /api/v1/games/{id}/guests                   — addGuest
DELETE /api/v1/games/{id}/guests                   — removeGuest
GET    /api/v1/games/{id}/participations           — getParticipations
GET    /api/v1/games/{id}/guests                   — getGuests
DELETE /api/v1/games/{id}/players/{telegramID}     — kickPlayer
DELETE /api/v1/games/{id}/guests/{guestID}         — kickGuest

GET /api/v1/players/{telegramID}                   — getPlayerByTelegramID
GET /api/v1/players/{telegramID}/next-game         — getNextGame
GET /api/v1/players/{playerID}/games               — listPlayerGames

PUT    /api/v1/groups/{chatID}                     — upsertGroup
PATCH  /api/v1/groups/{chatID}/language            — setGroupLanguage
PATCH  /api/v1/groups/{chatID}/timezone            — setGroupTimezone
DELETE /api/v1/groups/{chatID}                     — removeGroup
GET    /api/v1/groups                              — listGroups
GET    /api/v1/groups/{chatID}                     — getGroup

POST   /api/v1/venues                              — createVenue
GET    /api/v1/venues                              — listVenues (query: groupId)
GET    /api/v1/venues/{id}                         — getVenue
PATCH  /api/v1/venues/{id}                         — updateVenue
DELETE /api/v1/venues/{id}                         — deleteVenue; 409 Conflict if venue has active court_bookings

POST   /api/v1/venues/{id}/credentials             — addCredential (body: group_id, login, password, priority, max_courts); 503 when CREDENTIALS_ENCRYPTION_KEY unset
GET    /api/v1/venues/{id}/credentials             — listCredentials (query: group_id); passwords never returned
DELETE /api/v1/venues/{id}/credentials/{cid}       — removeCredential (query: group_id); 409 Conflict if credential has active court_bookings
GET    /api/v1/venues/{id}/credentials/priorities  — listCredentialPriorities (query: group_id)

POST /api/v1/scheduler/trigger/{event}             — triggerScheduler
```

---

## Service layer patterns

### GameService / ParticipationService / VenueService

- Constructed in `main.go`, injected into `api.Handler`
- Methods return domain types from `internal/models`
- `ParticipationService` calls `Notifier.EditGameMessage(ctx, gameID)` asynchronously after every join/skip/guest mutation — this fires a Telegram message edit in a background goroutine
- All write operations update the DB then notify; notification failures are logged but do not fail the API response

### Scheduler (`service/scheduler.go`)

- Runs a single 5-minute cron poll via `robfig/cron/v3`
- `RunScheduledTasks()` → calls `run(false)` on each of the 4 jobs in sequence
- `ForceRun(event string)` → calls `run(true)` on the named job (bypasses time-window gates)
- Each job is a struct implementing `scheduledJob` interface: `run(force bool)`, `name() string`
- Job names (for `/trigger` endpoint): `cancellation_reminder`, `booking_reminder`, `auto_booking`, `day_after_cleanup`

### Scheduled jobs

| Job | File | Window | Dedup guard |
|-----|------|--------|-------------|
| CancellationReminderJob | cancellation_reminder.go | ±2m30s of `game_date - (gracePeriod+6)h` | `notified_day_before` flag |
| BookingReminderJob | booking_reminder.go | [10:00, 10:05) group local time | `last_booking_reminder_at` per venue (date-scoped) |
| AutoBookingJob | auto_booking.go | [00:00, 00:05) group local time | `last_auto_booking_at` per venue (date-scoped) |
| DayAfterCleanupJob | day_after_cleanup.go | [03:00, 03:05) group local time | `completed` flag on game |

**Critical:** All time-window checks use `group.Timezone` (IANA string from `bot_groups`), resolved via `group_resolver.go`. Invalid timezone strings fall back to the service default (`TIMEZONE` env var).

### Scheduled job details

**CancellationReminderJob**: For each upcoming unnotified game, computes `reminderAt = game_date − (gracePeriod+6)h`. Checks capacity, attempts court cancellation via the booking service (if `SPORTS_BOOKING_SERVICE_URL` set), then **always** sends a group notification. `gracePeriodHours` defaults to 24 from the linked venue.

Court cancellation dispatch (`court_cancellation.go`):
- **Per-slot routing**: calls `autoBookingResultRepo.GetByGameID(gameID)` first. If result found with non-empty `game_time` → uses `courtBookingRepo.GetByVenueAndDateAndTime` (loads only entries for that time slot). If no result or empty `game_time` → falls back to `GetByVenueAndDate` (all entries for the day).
- **New path (credential-aware)**: if active `court_bookings` entries exist for venue+date[+time], calls `cancelUsingBookingEntries`. Each entry carries `credential_id`; `GetDecryptedByID` fetches login/password (nil → env-var fallback). Phase-1/Phase-2 selection runs on `court_label` values. Successful cancellations call `MarkCanceled`.
- **Legacy fallback**: no entries → calls `ListMatches(my=true)` for the game time window, cancels with empty credentials (env-var account).

Selection phases (both paths):
- **Phase 1 — priority order**: iterates `auto_booking_courts` in **reverse** (lowest-priority first), picks booked courts up to `courtsToCancel`. Legacy path calls `ListCourts` to build Eversports ID → name-number map; skipped if `ListCourts` fails.
- **Phase 2 — consecutive-grouping fallback**: splits remaining booked courts into consecutive runs, cancels from the end of the smallest group (tie-break: lowest first court number).

Per-court `CancelMatch` errors are collected in `courtCancellationResult.cancelErrors`. If cancellation fails (infrastructure error **or** any `CancelMatch` error), all group admins receive a silent DM with the error details in addition to the group notification.

Group notification scenarios:
- `all_good` — count ≥ newCapacity
- `canceled_balanced` — canceled and count == newCapacity
- `odd_no_cancel` — odd count, nothing canceled, 1 free spot
- `odd_canceled` — odd count, some canceled, 1 free spot
- `all_canceled` — all courts canceled, game will not happen
- `even_no_cancel` — even count < capacity, nothing canceled (booking service absent or no owned bookings)

**BookingReminderJob**: Fires in the `[10:00, 10:05)` window per group timezone for venues with matching `game_days`. Deduplicates via `last_booking_reminder_at` (date-scoped).

For venues with `auto_booking_enabled`:
1. `GetByVenueAndDate(venueID, targetDate)` → list of `AutoBookingResult` rows.
2. If empty → DM admins (no booking happened), mark `last_booking_reminder_at`.
3. For each result where `GameID == nil`: parse `result.GameTime`, create `Game`, call `SetGameID(resultID, gameID)`.
4. If ALL results already have `GameID != nil` → nothing to do (idempotent on retry), mark `last_booking_reminder_at`.

For venues without `auto_booking_enabled` (manual reminder): DM admins a booking reminder (venue name + `booking_opens_days`); mark `last_booking_reminder_at`.

**AutoBookingJob**: Fires in the `[00:00, 00:05)` window per group timezone for venues with `auto_booking_enabled = true`, `game_days`, and `preferred_game_times` configured. Deduplicates via `last_auto_booking_at`.

Algorithm (outer loop iterates each time slot in `preferred_game_times`):
1. `VenueCredentialService.ListForBooking(venueID, cooldown)` — loads all usable credentials **before** any Eversports network calls. No credentials → `notifyNoCredentials`, bail out. First credential's `Login`/`Password` are used for the list steps below.
2. For each `gameTime` in `strings.Split(venue.PreferredGameTimes, ",")`:
   a. `GetByVenueAndDateAndTime(venueID, date, gameTime)` → skip slot if non-nil (already booked, idempotent retry support).
   b. `ListCourts(date, firstLogin, firstPassword)` → all facility courts for game date.
   c. `ListMatches(date, HHMM, HHMM, false, firstLogin, firstPassword)` at exact `gameTime` — occupied courts; absent courts are free.
   d. `filterFreeCourts`: matches by name-extracted number (`extractCourtNumber("Court 7")→7`) against venue `courts`; falls back to all free courts if no name-numbers match. If `auto_booking_courts` is set, returns preferred courts in priority order.
   e. Credential rotation loop: books up to `min(cred.MaxCourts, remaining)` per credential. On per-credential error: `MarkError(credID)`, notify admins **with sound**, put court back, advance to next credential. `CREDENTIAL_ERROR_COOLDOWN` (default 24h) gates eligibility. If all credentials exhausted with courts remaining: notify admins silently.
   f. Saves `court_bookings` entries per booked court (with `game_time` set); saves `auto_booking_results` row with `game_time`; DMs all admins silently.
3. Sets `last_auto_booking_at` after the full per-venue loop completes.

Admin notification types: `notifyNoCredentials` (sound, no usable creds), `notifyCredentialError` (sound, per-credential failure with login + error + cooldown), `notifyCredentialsExhausted` (silent, all creds tried but courts remain).

**DayAfterCleanupJob**: Fires in the `[03:00, 03:05)` window. Fetches yesterday's uncompleted games per group. Unpins message, removes keyboard, marks game complete. If the game has a `venue_id`, calls `courtBookingRepo.MarkCanceledByVenueAndDate` to bulk-close any orphaned active `court_bookings` rows (soft-delete via `canceled_at = NOW()`).

### Localisation in scheduler jobs

Scheduler jobs resolve language via `groupLang(ctx, groupRepo, chatID)` (calls `GetByID` directly — no HTTP, works without a live Telegram service). Exception: `BookingReminderJob` admin DMs use the admin's Telegram `LanguageCode` via `userLocalizer` since those are personal messages.

### GameNotifier (`service/game_notifier.go`)

Implements `Notifier`. Called asynchronously by `ParticipationService`. Fetches game + participants + guests, formats message and keyboard via `internal/gameformat`, then calls `TelegramAPI.Request(EditMessageTextConfig{})`. Timezone resolved via `resolveGroupTimezone`.

---

## Storage layer patterns

- All repos receive `*pgxpool.Pool` at construction
- SQL uses `pgx/v5` directly (no ORM)
- Go 1.22 `net/http` routing (`{id}` path params); storage uses `$1, $2` positional params
- Migrations embedded via `migrations/migrations.go` (`go:embed *.sql`) and run at startup via `golang-migrate`
- Integration tests in `storage/*_test.go` use `+build integration` tag; run with `go test -tags integration -timeout 120s ./...`

---

## Database schema (key columns for planning changes)

```sql
games:              id, chat_id, message_id, game_date, courts_count, courts,
                    venue_id (FK→venues ON DELETE SET NULL), notified_day_before, completed
players:            id, telegram_id UNIQUE, username, first_name, last_name
game_participations: game_id, player_id, status ('registered'|'skipped'), UNIQUE(game_id,player_id)
guest_participations: id, game_id, invited_by_player_id
bot_groups:         chat_id PK, title, bot_is_admin, language DEFAULT 'en', timezone DEFAULT 'UTC'
venues:             id, group_id FK→bot_groups, name, courts, time_slots, address,
                    grace_period_hours DEFAULT 24, game_days, booking_opens_days DEFAULT 14,
                    last_booking_reminder_at, preferred_game_times TEXT DEFAULT '', last_auto_booking_at,
                    auto_booking_courts, auto_booking_enabled DEFAULT FALSE, UNIQUE(group_id, name)
venue_credentials:  id, venue_id FK→venues ON DELETE CASCADE, login, enc_password (AES-256-GCM),
                    priority DEFAULT 0, max_courts DEFAULT 3, last_error_at (nullable TIMESTAMPTZ),
                    created_at, UNIQUE(venue_id, login)
court_bookings:     id BIGSERIAL PK, venue_id FK→venues ON DELETE CASCADE, game_date DATE,
                    court_uuid TEXT, court_label TEXT (name-extracted number, e.g. "7"),
                    match_id TEXT UNIQUE, booking_uuid TEXT,
                    credential_id BIGINT FK→venue_credentials ON DELETE SET NULL (NULL = env-var creds),
                    game_time TEXT NOT NULL DEFAULT '' (HH:MM of session; empty for legacy rows),
                    canceled_at TIMESTAMPTZ (NULL = active; set by MarkCanceled soft-delete),
                    created_at TIMESTAMPTZ DEFAULT NOW()
                    INDEX: (venue_id, game_date)
auto_booking_results: id, venue_id FK→venues ON DELETE CASCADE, game_date DATE,
                    game_time TEXT NOT NULL DEFAULT '' (HH:MM; empty for legacy rows),
                    courts (comma-sep court numbers), courts_count INT,
                    game_id BIGINT FK→games ON DELETE SET NULL (NULL = game not yet created by BookingReminderJob),
                    created_at, UNIQUE(venue_id, game_date, game_time)
```

Adding a new column always requires a new migration file in `migrations/`. Test DB must be truncated via `testutil.TruncateTables` which lists tables explicitly.

---

## Environment variables

```
TELEGRAM_BOT_TOKEN=           required (scheduler sends Telegram messages)
DATABASE_URL=                 required (PostgreSQL connection string)
INTERNAL_API_SECRET=          required (authenticates calls from telegram bot)
SERVER_PORT=8080              default
CRON_POLL=*/5 * * * *        how often to poll for scheduled tasks
LOG_LEVEL=INFO
LOG_DIR=                      optional; writes $LOG_DIR/app.log (10 MB / 5 backups, gzip) + stdout
TIMEZONE=UTC
SPORTS_BOOKING_SERVICE_URL=   optional; enables auto court cancellation + auto booking
CREDENTIALS_ENCRYPTION_KEY=   optional; 64 hex chars (AES-256-GCM) for venue booking credentials at rest; 503 when unset
CREDENTIAL_ERROR_COOLDOWN=24h how long a failed credential is skipped before retry
```

---

## Constraints and conventions

- New business rules go in `service/`, not `api/`
- HTTP handlers in `api/` only validate input, call service methods, and write responses
- New scheduled logic requires a new job struct in `service/` registered in `scheduler.go`
- `ParticipationService.Notifier` call pattern: always async (`go notifier.EditGameMessage(...)`) so a slow Telegram API never blocks the HTTP response
- `BookingServiceClient` interface in `booking_client.go`: `ListCourts(ctx, date, login, password string)`, `ListMatches(ctx, date, startTime, endTime string, my bool, login, password string)`, `CancelMatch(ctx, matchUUID, login, password string) error`, `BookMatch(ctx, courtUUID, start, end, login, password string)` — `login`/`password` select a per-credential Eversports session on the booking service (forwarded as `X-Eversports-Email`/`-Password` headers); empty strings fall back to the service-level default (env-var) account
- `VenueCredentialService` is injected into `AutoBookingJob`; `credCooldown` (`CREDENTIAL_ERROR_COOLDOWN`, default 24h) gates which credentials are eligible; `MarkError` sets `last_error_at` on failure
- Version is read from `cmd/management/VERSION` file, injected at build time via `-ldflags "-X main.Version=<ver>"`

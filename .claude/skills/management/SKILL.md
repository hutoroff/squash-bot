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
                   MarkCanceled(ctx, matchID) — soft-delete: sets canceled_at = NOW(),
                   HasActiveByCredentialID(ctx, credentialID) bool,
                   HasActiveByVenueID(ctx, venueID) bool
```

`VenueCredentialService` (`service/venue_credential_service.go`) wraps the repository with:
- `Add(ctx, venueID, groupID, login, password, priority, maxCourts)` — validates venue ownership, deduplicates by login, encrypts password via `Encryptor`, stores via repo
- `List(ctx, venueID, groupID)` — validates ownership, returns credentials without passwords
- `Remove(ctx, credID, venueID, groupID)` — validates ownership; returns `ErrCredentialInUse` if `courtBookingRepo.HasActiveByCredentialID` is true; deletes
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
                    last_booking_reminder_at, preferred_game_time, last_auto_booking_at,
                    auto_booking_courts, auto_booking_enabled DEFAULT FALSE, UNIQUE(group_id, name)
venue_credentials:  id, venue_id FK→venues ON DELETE CASCADE, login, enc_password (AES-256-GCM),
                    priority DEFAULT 0, max_courts DEFAULT 3, last_error_at (nullable TIMESTAMPTZ),
                    created_at, UNIQUE(venue_id, login)
court_bookings:     id BIGSERIAL PK, venue_id FK→venues ON DELETE CASCADE, game_date DATE,
                    court_uuid TEXT, court_label TEXT (name-extracted number, e.g. "7"),
                    match_id TEXT UNIQUE, booking_uuid TEXT,
                    credential_id BIGINT FK→venue_credentials ON DELETE SET NULL (NULL = env-var creds),
                    canceled_at TIMESTAMPTZ (NULL = active; set by MarkCanceled soft-delete),
                    created_at TIMESTAMPTZ DEFAULT NOW()
                    INDEX: (venue_id, game_date)
```

Adding a new column always requires a new migration file in `migrations/`. Test DB must be truncated via `testutil.TruncateTables` which lists tables explicitly.

---

## Constraints and conventions

- New business rules go in `service/`, not `api/`
- HTTP handlers in `api/` only validate input, call service methods, and write responses
- New scheduled logic requires a new job struct in `service/` registered in `scheduler.go`
- `ParticipationService.Notifier` call pattern: always async (`go notifier.EditGameMessage(...)`) so a slow Telegram API never blocks the HTTP response
- `BookingServiceClient` interface in `booking_client.go`: methods are `ListMatches`, `CancelMatch(ctx, matchUUID, login, password string) error`, `BookMatch(ctx, courtUUID, start, end, login, password string)` — `login`/`password` select a per-credential Eversports session on the booking service; empty strings fall back to the service-level default account
- `VenueCredentialService` is injected into `AutoBookingJob`; `credCooldown` (`CREDENTIAL_ERROR_COOLDOWN`, default 24h) gates which credentials are eligible; `MarkError` sets `last_error_at` on failure
- Version is read from `cmd/management/VERSION` file, injected at build time via `-ldflags "-X main.Version=<ver>"`

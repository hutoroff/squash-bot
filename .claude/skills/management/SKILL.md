---
name: management
description: Architecture reference for the squash-bot management service (cmd/management). Load before planning any changes to API handlers, business logic, scheduler jobs, or storage layer.
user-invocable: true
---

# Management Service — Architecture Reference

The management service is the central data hub. It owns the PostgreSQL database, all business logic, and the cron scheduler. The telegram bot and web service call it over HTTP; it never calls them back (the `Notifier` interface is injected at startup via `TelegramAPI`).

**Entry point:** `cmd/management/main.go` (thin composition root)
**Port:** 8080 (env `SERVER_PORT`)
**Module path:** `github.com/hutoroff/squash-bot`

---

## Hexagonal architecture overview

The service follows the **Ports & Adapters** pattern (also called hexagonal architecture). The mental model:

```
          ┌─────────────────────────────────────────┐
          │           Application Core               │
          │                                         │
  HTTP ──►│  inbound port  →  domain logic          │
  (REST)  │  (interface)       (service structs)    │
          │                         │               │
          │              outbound port              │
          │              (interface)                │
          └─────────────────────────────────────────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
                 Postgres           Booking API
                 (outbound          (outbound
                  adapter)           adapter)
```

**Three zones:**

| Zone | Location | Rule |
|------|----------|------|
| **Application core** | `internal/management/application/` | Pure business logic. Imports only `internal/models` and port interfaces. Never imports adapters. |
| **Inbound ports** | `application/ports/inbound/` | Go interfaces defining what the core *offers* to callers. One file per aggregate. The HTTP adapter depends on these. |
| **Outbound ports** | `application/ports/outbound/` | Go interfaces defining what the core *needs* from infrastructure. One file per concern. Implemented by adapters. |
| **Inbound adapters** | `adapters/inbound/http/` | Translate HTTP requests → port calls. Import inbound port interfaces only. |
| **Outbound adapters** | `adapters/outbound/{postgres,booking,telegram,crypto}/` | Implement outbound port interfaces. Do all I/O. |
| **Composition root** | `cmd/management/main.go` | The only file that imports every layer. Wires adapters to ports via constructor injection. |

**Key constraints that must be preserved:**
- Application packages (`game/`, `participation/`, `venue/`, `group/`, `player/`, `audit/`, `scheduler/`) **never** import anything from `adapters/`.
- Inbound adapters (`adapters/inbound/http/`) import `ports/inbound` interfaces and application error sentinels only — not concrete service types.
- Outbound adapters (`adapters/outbound/`) implement outbound port interfaces and import `internal/models` — not each other.
- `main.go` is the single point of wiring. If you find yourself adding cross-layer imports elsewhere, stop and reconsider.

**How to add a new operation (the correct sequence):**
1. Add the method to the relevant outbound port interface (e.g., `ports/outbound/game_repo.go`) if new storage access is needed.
2. Implement it in the outbound adapter (e.g., `adapters/outbound/postgres/game_repo.go`).
3. Add the method to the relevant inbound port interface (e.g., `ports/inbound/games.go`) if the HTTP layer needs it.
4. Implement it in the application service (e.g., `application/game/service.go`).
5. Wire any new handler in `adapters/inbound/http/` and register the route in `server.go`.
6. No changes to `main.go` are needed unless a new constructor argument is required.

---

## Package structure

```
cmd/management/
└── main.go                          — composition root only: wire deps, start HTTP + cron

internal/management/
├── application/
│   ├── ports/
│   │   ├── inbound/                 — use-case interfaces (one file per aggregate)
│   │   │   ├── games.go             — GameUseCases
│   │   │   ├── participations.go    — ParticipationUseCases
│   │   │   ├── venues.go            — VenueUseCases
│   │   │   ├── venue_credentials.go — VenueCredentialUseCases
│   │   │   ├── groups.go            — GroupUseCases
│   │   │   ├── players.go           — PlayerUseCases
│   │   │   ├── scheduler.go         — SchedulerUseCases
│   │   │   ├── audit.go             — AuditUseCases
│   │   │   └── admin_resolver.go    — AdminGroupsResolver
│   │   └── outbound/                — repository/adapter interfaces (one file per concern)
│   │       ├── game_repo.go         — GameRepository
│   │       ├── player_repo.go       — PlayerRepository
│   │       ├── participation_repo.go — ParticipationRepository
│   │       ├── guest_repo.go        — GuestRepository
│   │       ├── group_repo.go        — GroupRepository
│   │       ├── venue_repo.go        — VenueRepository
│   │       ├── venue_credential_repo.go — VenueCredentialRepository + DecryptedCredential
│   │       ├── court_booking_repo.go — CourtBookingRepository
│   │       ├── auto_booking_result_repo.go — AutoBookingResultRepository
│   │       ├── audit_event_repo.go  — AuditEventRepository
│   │       ├── service_state_repo.go — ServiceStateRepository
│   │       ├── booking_client.go    — BookingServiceClient + BookingCourt, BookingSlot, BookMatchResult
│   │       ├── telegram.go          — TelegramAPI
│   │       ├── notifier.go          — Notifier
│   │       └── encryptor.go         — Encryptor
│   ├── game/
│   │   └── service.go               — GameService (implements GameUseCases)
│   ├── participation/
│   │   └── service.go               — ParticipationService (implements ParticipationUseCases)
│   ├── venue/
│   │   ├── service.go               — VenueService + ErrVenueHasActiveBookings
│   │   └── credential_service.go    — VenueCredentialService + ErrDuplicateCredentialLogin,
│   │                                  ErrCredentialInUse, ErrCredentialNotFound
│   ├── group/
│   │   └── service.go               — GroupService (implements GroupUseCases)
│   ├── player/
│   │   └── service.go               — PlayerService (implements PlayerUseCases)
│   ├── audit/
│   │   ├── service.go               — AuditService (implements AuditUseCases)
│   │   └── admin_groups_resolver.go — AdminGroupsResolver (implements inbound.AdminGroupsResolver)
│   ├── changelog/
│   │   └── announcer.go             — AnnounceChangelog: send new-version changelog on startup
│   └── scheduler/
│       ├── scheduler.go             — Scheduler: RunScheduledTasks, ForceRun, HasJob
│       ├── auto_booking.go          — AutoBookingJob
│       ├── booking_reminder.go      — BookingReminderJob
│       ├── cancellation_reminder.go — CancellationReminderJob
│       ├── court_cancellation.go    — court cancellation helpers (used by CancellationReminderJob)
│       ├── day_after_cleanup.go     — DayAfterCleanupJob
│       └── group_resolver.go        — groupTZByID, groupLang, resolveGroupTimezone helpers
├── adapters/
│   ├── inbound/
│   │   └── http/
│   │       ├── server.go            — Handler struct, NewHandler, RegisterRoutes, NewServer, Run
│   │       ├── games.go             — game HTTP handlers + parseID, parseChatIDs helpers
│   │       ├── participations.go    — participation HTTP handlers
│   │       ├── players.go           — player HTTP handlers
│   │       ├── groups.go            — group HTTP handlers
│   │       ├── venues.go            — venue HTTP handlers
│   │       ├── venue_credentials.go — venue credential HTTP handlers
│   │       ├── scheduler.go         — POST /api/v1/scheduler/trigger/{event}
│   │       └── audit.go             — GET /api/v1/audit (listAuditEvents)
│   └── outbound/
│       ├── postgres/
│       │   ├── postgres.go          — NewPool (pgxpool)
│       │   ├── game_repo.go         — GameRepo
│       │   ├── player_repo.go       — PlayerRepo
│       │   ├── participation_repo.go — ParticipationRepo
│       │   ├── guest_repo.go        — GuestRepo
│       │   ├── group_repo.go        — GroupRepo
│       │   ├── venue_repo.go        — VenueRepo
│       │   ├── venue_credential_repo.go — VenueCredentialRepo
│       │   ├── court_booking_repo.go — CourtBookingRepo
│       │   ├── auto_booking_result_repo.go — AutoBookingResultRepo
│       │   ├── audit_event_repo.go  — AuditEventRepo
│       │   └── service_state_repo.go — ServiceStateRepo
│       ├── booking/
│       │   └── client.go            — HTTPBookingClient implements outbound.BookingServiceClient
│       ├── telegram/
│       │   ├── notifier.go          — GameNotifier implements outbound.Notifier
│       │   └── helpers.go           — formatting helpers for Telegram messages
│       └── crypto/
│           └── encryptor.go         — AES-256-GCM Encryptor implements outbound.Encryptor
```

---

## Optional dependencies and nil-safety

Two outbound dependencies are optional — their ports are set to nil when not configured:

| Port type | Nil when | Nil-checked by |
|-----------|----------|----------------|
| `outbound.BookingServiceClient` | `SPORTS_BOOKING_SERVICE_URL` not set | `AutoBookingJob.run()`, `CancellationReminderJob` |
| `inbound.VenueCredentialUseCases` | `CREDENTIALS_ENCRYPTION_KEY` not set | HTTP `credServiceAvailable()`, `AutoBookingJob.processTimeSlot()` |

When adding new code that uses either, always guard with `if x == nil { return/skip }` before the first call. Do **not** assume they are always populated.

---

## Inbound port interfaces (`application/ports/inbound/`)

One file per aggregate. Exact method signatures:

```go
// games.go
type GameUseCases interface {
    CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error)
    GetByID(ctx context.Context, id int64) (*models.Game, error)
    GetUpcomingGames(ctx context.Context) ([]*models.Game, error)
    GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error)
    UpdateMessageID(ctx context.Context, gameID, messageID int64) error
    UpdateCourts(ctx context.Context, gameID int64, courts string) error
}

// participations.go
type ParticipationUseCases interface {
    Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error)
    Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error)
    AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
    RemoveGuest(ctx context.Context, gameID, telegramID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
    GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
    GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
    GetRegisteredCount(ctx context.Context, gameID int64) (int, error)
    GetGuestCount(ctx context.Context, gameID int64) (int, error)
    KickPlayer(ctx context.Context, gameID, telegramID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
    KickGuestByID(ctx context.Context, gameID, guestID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
}

// venues.go
type VenueUseCases interface {
    CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error)
    GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error)
    GetVenueByID(ctx context.Context, id int64) (*models.Venue, error)
    UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error)
    DeleteVenue(ctx context.Context, id, groupID int64) error
}

// venue_credentials.go
type VenueCredentialUseCases interface {
    Add(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int) (*models.VenueCredential, error)
    List(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error)
    Remove(ctx context.Context, credentialID, venueID, groupID int64) error
    PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error)
    GetDecryptedByID(ctx context.Context, credID int64) (*outbound.DecryptedCredential, error)
    ListForBooking(ctx context.Context, venueID int64, cooldown time.Duration) ([]outbound.DecryptedCredential, error)
    MarkError(ctx context.Context, credID int64) error
}

// groups.go
type GroupUseCases interface {
    Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error
    SetLanguage(ctx context.Context, chatID int64, language string) error
    SetTimezone(ctx context.Context, chatID int64, timezone string) error
    SetChangelogEnabled(ctx context.Context, chatID int64, enabled bool) error
    Remove(ctx context.Context, chatID int64) error
    Exists(ctx context.Context, chatID int64) (bool, error)
    GetByID(ctx context.Context, chatID int64) (*models.Group, error)
    GetAll(ctx context.Context) ([]models.Group, error)
}

// players.go
type PlayerUseCases interface {
    GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error)
    Upsert(ctx context.Context, player *models.Player) (*models.Player, error)
    GetNextGame(ctx context.Context, telegramID int64) (*models.Game, error)
    ListGames(ctx context.Context, playerID int64) ([]models.PlayerGame, error)
}

// scheduler.go
type SchedulerUseCases interface {
    RunScheduledTasks()
    ForceRun(event string)
    HasJob(event string) bool
}

// admin_resolver.go
type AdminGroupsResolver interface {
    AdminGroupsFor(ctx context.Context, tgID int64) ([]int64, error)
}
```

---

## Outbound port interfaces (`application/ports/outbound/`)

```go
// booking_client.go — all methods pass login+password per-call (no session state)
type BookingServiceClient interface {
    ListCourts(ctx context.Context, date, login, password string) ([]BookingCourt, error)
    ListMatches(ctx context.Context, date, startTime, endTime string, my bool, login, password string) ([]BookingSlot, error)
    CancelMatch(ctx context.Context, matchUUID, login, password string) error
    BookMatch(ctx context.Context, courtUUID, start, end, login, password string) (*BookMatchResult, error)
}

// telegram.go
type TelegramAPI interface {
    Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
    Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
    GetChatAdministrators(config tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error)
}

// notifier.go
type Notifier interface {
    EditGameMessage(ctx context.Context, game *models.Game) error
}

// encryptor.go
type Encryptor interface {
    Encrypt(plaintext string) (string, error)
    Decrypt(ciphertext string) (string, error)
}
```

Value types in `outbound` package: `BookingCourt{ID, UUID, Name}`, `BookingSlot{Court, CourtUUID, IsUserBookingOwner, Present, Title, Booking, Match}`, `BookMatchResult{BookingUUID, BookingID, MatchID}`, `DecryptedCredential{ID, VenueID, Login, Password, Priority, MaxCourts}`.

---

## HTTP Handler (`adapters/inbound/http/server.go`)

```go
type Handler struct {
    gameSvc        inbound.GameUseCases
    partSvc        inbound.ParticipationUseCases
    venueSvc       inbound.VenueUseCases
    venueCredSvc   inbound.VenueCredentialUseCases  // nil when encryption disabled
    groupSvc       inbound.GroupUseCases
    playerSvc      inbound.PlayerUseCases
    scheduler      inbound.SchedulerUseCases
    auditSvc       inbound.AuditUseCases
    adminResolver  inbound.AdminGroupsResolver
    serverOwnerIDs map[int64]bool
    logger         *slog.Logger
    version        string
}
```

All handlers are methods on `*Handler` using only inbound port interfaces — no concrete types, no direct DB access.

---

## Route table

All routes registered in `RegisterRoutes`. Bearer auth (`requireBearer`) applied to all non-`/version` routes.

| Method | Path | Handler |
|--------|------|---------|
| GET | `/version` | inline |
| POST | `/api/v1/games` | createGame |
| GET | `/api/v1/games/{id}` | getGame |
| PATCH | `/api/v1/games/{id}/courts` | updateGameCourts |
| PATCH | `/api/v1/games/{id}/venue` | updateGameVenue |
| DELETE | `/api/v1/games/{id}` | deleteGame |
| GET | `/api/v1/games` | listGames |
| POST | `/api/v1/games/{id}/participate` | joinGame |
| POST | `/api/v1/games/{id}/skip` | skipGame |
| POST | `/api/v1/games/{id}/guests` | addGuest |
| DELETE | `/api/v1/games/{id}/guests/{gid}` | removeGuest |
| POST | `/api/v1/games/{id}/kick/{pid}` | kickPlayer |
| POST | `/api/v1/games/{id}/kick-guest/{gid}` | kickGuest |
| GET | `/api/v1/players/{tgid}` | getPlayerByTelegramID |
| GET | `/api/v1/players/{tgid}/next-game` | getNextGame |
| GET | `/api/v1/players/{tgid}/games` | listPlayerGames |
| POST | `/api/v1/groups` | upsertGroup |
| DELETE | `/api/v1/groups/{chatID}` | removeGroup |
| GET | `/api/v1/groups/{chatID}` | getGroup |
| GET | `/api/v1/groups` | listGroups |
| PATCH | `/api/v1/groups/{chatID}/timezone` | updateGroupTimezone |
| PATCH | `/api/v1/groups/{chatID}/language` | updateGroupLanguage |
| POST | `/api/v1/venues` | createVenue |
| GET | `/api/v1/venues` | listVenues |
| GET | `/api/v1/venues/{id}` | getVenue |
| PUT | `/api/v1/venues/{id}` | updateVenue |
| DELETE | `/api/v1/venues/{id}` | deleteVenue |
| GET | `/api/v1/venues/{id}/credentials` | listCredentials |
| POST | `/api/v1/venues/{id}/credentials` | addCredential |
| DELETE | `/api/v1/venues/{id}/credentials/{cid}` | removeCredential |
| GET | `/api/v1/venues/{id}/credential-priorities` | listCredentialPriorities |
| POST | `/api/v1/scheduler/trigger/{event}` | triggerScheduler |
| GET | `/api/v1/audit` | listAuditEvents |

---

## Scheduler jobs

All jobs registered via `appsched.NewScheduler(logger, job1, ...)` in `main.go`.

| Job struct | Event name | Timing window |
|------------|-----------|---------------|
| `CancellationReminderJob` | `cancellation_reminder` | ±2m30s of game time |
| `BookingReminderJob` | `booking_reminder` | [10:00, 10:05) group TZ |
| `DayAfterCleanupJob` | `day_after_cleanup` | [03:00, 03:05) group TZ |
| `AutoBookingJob` | `auto_booking` | [00:00, 00:05) group TZ |

`POST /api/v1/scheduler/trigger/{event}` forces any job by name (202 Accepted, runs in goroutine).

`AutoBookingJob` skips if `bookingClient == nil` (no `SPORTS_BOOKING_SERVICE_URL`) or if `credService == nil` (no `CREDENTIALS_ENCRYPTION_KEY`).

---

## Error sentinels

```go
// application/venue/service.go
var ErrVenueHasActiveBookings = errors.New("venue has active court bookings and cannot be deleted")

// application/venue/credential_service.go
var ErrDuplicateCredentialLogin = errors.New("a credential with this login already exists for this venue")
var ErrCredentialInUse          = errors.New("credential has active court bookings and cannot be removed")
var ErrCredentialNotFound       = errors.New("credential not found")
```

HTTP adapter maps these to status codes via `errors.Is`.

---

## AuditService event types

| Event type | Visibility | Triggered by |
|---|---|---|
| `game.created` | group_admin | Admin creates game |
| `game.courts_reserved` | group_admin | Admin updates courts |
| `participation.joined` | player | Player joins |
| `participation.skipped` | player | Player skips |
| `participation.guest_added` | player | Player adds guest |
| `participation.guest_removed` | player | Player removes guest |
| `participation.player_kicked` | group_admin | Admin kicks player |
| `participation.guest_kicked` | group_admin | Admin kicks guest |
| `credential.added` | group_admin | Admin adds booking credential |
| `credential.removed` | group_admin | Admin removes booking credential |
| `venue.created` | group_admin | Admin creates venue |
| `venue.updated` | group_admin | Admin updates venue |
| `venue.deleted` | group_admin | Admin deletes venue |
| `court.booked` | group_admin | Court booked (scheduler or admin via BookGameCourts) |
| `court.canceled` | group_admin | Scheduler cancels a court |
| `game.published` | group_admin | Game announcement sent to group (by scheduler or admin) |

Visibility hierarchy: `server_owner` ≥ `group_admin` ≥ `player`. Non-owners see only their own player events plus admin events for groups they administrate (resolved via `AdminGroupsResolver`).

---

## Key env vars

| Var | Required | Default | Notes |
|-----|----------|---------|-------|
| `DATABASE_URL` | yes | — | PostgreSQL DSN |
| `TELEGRAM_BOT_TOKEN` | yes | — | Scheduler sends Telegram messages |
| `INTERNAL_API_SECRET` | yes | — | Bearer token for all API routes |
| `SERVER_PORT` | no | `8080` | HTTP listen port |
| `CRON_POLL` | no | `*/5 * * * *` | Must be `*/N * * * *` pattern |
| `CREDENTIALS_ENCRYPTION_KEY` | no | — | AES-256 key; disables credential mgmt if unset |
| `SPORTS_BOOKING_SERVICE_URL` | no | — | Enables auto-booking + cancellation |
| `SERVICE_ADMIN_IDS` | no | — | Comma-sep Telegram IDs with `/trigger` access |
| `AUDIT_RETENTION_DAYS` | no | `365` | Days to keep audit events |
| `CREDENTIAL_ERROR_COOLDOWN` | no | — | Duration: skip errored credentials for this long |
| `TIMEZONE` | no | `UTC` | Default location for scheduler |
| `LOG_LEVEL` | no | `INFO` | `DEBUG` enables verbose logging |
| `LOG_DIR` | no | — | If set, also write logs to `LOG_DIR/app.log` |

---

## DB schema (key tables)

```
games(id, chat_id, message_id, game_date, courts_count, courts, venue_id→venues, notified_day_before, completed, created_at)
players(id, telegram_id UNIQUE, username, first_name, last_name, created_at)
game_participations(id, game_id, player_id, status, created_at)
guest_participations(id, game_id, invited_by_player_id, created_at)
bot_groups(chat_id PK, title, bot_is_admin, language DEFAULT 'en', timezone DEFAULT 'UTC', added_at)
venues(id, group_id→bot_groups CASCADE, name, courts, time_slots, address, grace_period_hours DEFAULT 24,
       game_days, booking_opens_days DEFAULT 14, preferred_game_time, auto_booking_courts,
       auto_booking_enabled, auto_booking_games_count, last_booking_reminder_at, last_auto_booking_at, created_at)
venue_credentials(id, venue_id→venues, group_id→bot_groups, login_enc, password_enc, priority, max_courts,
                  last_error_at, created_at)
court_bookings(id, venue_id, group_id, booking_uuid, court_number, start_time, end_time, created_at)
auto_booking_results(id, venue_id, group_id, game_date, status, detail, created_at)
audit_events(id, event_type, actor_tg_id, actor_display, group_id, entity_id, detail, occurred_at)
service_state(key PK, value, updated_at)
```

Migrations embedded via `migrations/migrations.go` (`go:embed *.sql`) and run at startup.

---

## Libraries

- `github.com/jackc/pgx/v5` + pgxpool (max 10 conns)
- `github.com/golang-migrate/migrate/v4` with iofs source
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1
- `github.com/robfig/cron/v3`
- `github.com/caarlos0/env/v10`
- `gopkg.in/natefinch/lumberjack.v2` (log rotation)

Use `PinChatMessageConfig{}` struct — `NewPinChatMessage()` does not exist in v5.

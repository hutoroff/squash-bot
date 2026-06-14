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
│   ├── games.go       — game HTTP handlers
│   ├── participations.go — participation HTTP handlers; kick endpoints accept audit query params
│   ├── groups.go      — group HTTP handlers; upsertGroup/removeGroup accept actor audit params
│   ├── venues.go      — venue HTTP handlers
│   └── audit.go       — GET /api/v1/audit (listAuditEvents); enforces visibility per caller
└── service/
│   ├── interfaces.go           — ALL repository + Telegram interfaces (source of truth)
│   ├── game_service.go         — GameService: Create, GetByID, UpdateCourts, …
│   ├── participation_service.go — ParticipationService: Join, Skip, AddGuest, RemoveGuest, KickPlayer, KickGuest
│   ├── venue_service.go        — VenueService: Create, GetByGroup, Update, Delete
│   ├── audit_service.go        — AuditService: 15+ Record* methods, Query, RunRetention
│   ├── changelog_announcer.go  — AnnounceChangelog: on startup, send new-version changelog to opted-in groups
│   ├── admin_groups_resolver.go — AdminGroupsResolver: AdminGroupsFor(ctx, tgID) resolves which groups
│   │                              a caller administers (satisfies api.adminGroupsResolver interface)
│   ├── game_notifier.go        — GameNotifier (implements Notifier): EditGameMessage
│   ├── scheduler.go            — Scheduler: RunScheduledTasks, registers jobs
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
    ├── court_booking_repo.go    — CourtBookingRepo implements CourtBookingRepository
    ├── audit_event_repo.go      — AuditEventRepo implements AuditEventRepository
    └── service_state_repo.go    — ServiceStateRepo implements ServiceStateRepository (KV store)
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
GroupRepository  — Upsert, SetLanguage, SetTimezone, SetChangelogEnabled, SetAutoBookingAllowed, Remove, Exists, GetByID, GetAll
ServiceStateRepository — Get(ctx, key) (string, error), Set(ctx, key, value string) error
                   — backed by `service_state` table (TEXT key PK, TEXT value); `pgx.ErrNoRows` when key absent
VenueRepository  — Create, GetByID, GetByIDAndGroupID, GetByGroupID, Update, Delete,
                   SetLastBookingReminderAt, SetLastAutoBookingAt
VenueCredentialRepository — Create(venueID, login, encPassword, priority, maxCourts),
                   ListByVenueID, ListWithPasswordByVenueID, GetWithPasswordByID(id),
                   Delete(id, venueID), ExistsByLogin(venueID, login),
                   PrioritiesInUse(venueID), SetLastErrorAt(id)
AuditEventRepository — Insert(ctx, *models.AuditEvent),
                   Query(ctx, models.AuditQueryFilter) []*models.AuditEvent,
                   DeleteOlderThan(ctx, cutoff time.Time) int64
CourtBookingRepository — Save(ctx, *models.CourtBooking),
                   GetByVenueAndDate(ctx, venueID, gameDate) — returns only active (canceled_at IS NULL),
                   GetByVenueAndDateAndTime(ctx, venueID int64, date time.Time, gameTime string) — active rows filtered by game_time column,
                   MarkCanceled(ctx, matchID) — soft-delete: sets canceled_at = NOW(),
                   MarkCanceledByVenueAndDate(ctx, venueID, gameDate) — bulk soft-delete all active rows for venue+date,
                   HasActiveByCredentialID(ctx, credentialID) bool,
                   HasActiveByVenueID(ctx, venueID) bool,
                   GetActiveByVenueDateAndLabels(ctx, venueID int64, gameDate time.Time, labels []string) — active rows whose court_label matches one of the given labels; used by the manual court-removal flow
AutoBookingResultRepository — Save(ctx, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int) (int64, error) — returns the saved row ID (used by AutoBookingJob to call SetGameID immediately),
                   GetByVenueAndDate(ctx, venueID int64, date time.Time) []*models.AutoBookingResult,
                   GetByVenueAndDateAndTime(ctx, venueID int64, date time.Time, gameTime string) *models.AutoBookingResult — nil if not yet booked,
                   GetByGameID(ctx, gameID int64) *models.AutoBookingResult — used by cancellation to find game_time,
                   SetGameID(ctx, resultID, gameID int64) — links result to a game; called by both AutoBookingJob (at 00:00) and BookingReminderJob (legacy path)
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
PATCH  /api/v1/games/{id}/courts                   — updateCourts; when body contains cancel_bookings=true,
                                                     calls RemoveCourtsAndCancelBookings instead: 204 on full
                                                     success, 200+JSON {canceled:[],failed:[{court,reason}]}
                                                     on partial failure
GET    /api/v1/games/{id}/active-court-bookings    — listActiveCourtBookings; query: courts (comma-sep labels);
                                                     returns [{court_label,game_time,match_id}] for active bookings
POST   /api/v1/games/{id}/book-courts              — bookCourts; body: {count int, group_id, actor_telegram_id, actor_display};
                                                     409 (ErrAutoBookingNotAvailable) when venue has no auto-booking or no usable credentials;
                                                     returns {requested, booked_count, booked_labels, failures};
                                                     credCooldown defaults to 24h (defaultCredentialErrorCooldown in server.go)
POST   /api/v1/games/{id}/publish                  — publishGame; body: actor_telegram_id, actor_display;
                                                     404 if not found, 409 if already published, 502 on Telegram send failure

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
PATCH  /api/v1/groups/{chatID}/changelog                 — setGroupChangelog (body: changelog_enabled bool, actor fields)
PATCH  /api/v1/groups/{chatID}/leaderboard-notifications — setGroupLeaderboardNotifications (body: leaderboard_notifications_enabled bool, actor fields)
PATCH  /api/v1/groups/{chatID}/auto-booking-allowed      — setGroupAutoBookingAllowed (body: enabled bool, actor fields);
                                                     no-op 204 if value unchanged; on disable: cascades
                                                     auto_booking_enabled→false on all group venues (transactional)
DELETE /api/v1/groups/{chatID}                     — removeGroup
GET    /api/v1/groups                              — listGroups (response includes added_at)
GET    /api/v1/groups/{chatID}                     — getGroup (response includes added_at)

POST   /api/v1/venues                              — createVenue; rejects auto_booking_enabled=true
                                                     when group auto_booking_allowed=false (400)
GET    /api/v1/venues                              — listVenues (query: groupId)
GET    /api/v1/venues/{id}                         — getVenue
PATCH  /api/v1/venues/{id}                         — updateVenue; same auto_booking_allowed guard as create
DELETE /api/v1/venues/{id}                         — deleteVenue; 409 Conflict if venue has active court_bookings
GET    /api/v1/venues/{id}/booking-readiness       — bookingReadiness; query: group_id (required, enforces ownership);
                                                     200 {ready bool, max_courts int, reason string};
                                                     reason: "credentials_not_configured" | "auto_booking_disabled" |
                                                     "auto_booking_disallowed_by_owner" | "no_usable_credentials" | ""

POST   /api/v1/venues/{id}/credentials             — addCredential (body: group_id, login, password, priority, max_courts); 503 when CREDENTIALS_ENCRYPTION_KEY unset
GET    /api/v1/venues/{id}/credentials             — listCredentials (query: group_id); passwords never returned
DELETE /api/v1/venues/{id}/credentials/{cid}       — removeCredential (query: group_id); 409 Conflict if credential has active court_bookings
GET    /api/v1/venues/{id}/credentials/priorities  — listCredentialPriorities (query: group_id)

POST   /api/v1/game-results                        — submitGameResult
GET    /api/v1/game-results/{id}                   — getGameResult
POST   /api/v1/game-results/{id}/approval-message  — setGameResultApprovalMessage
POST   /api/v1/game-results/{id}/approve           — approveGameResult; synchronously applies Glicko-2
                                                     in the same tx as the status flip (DecideInTx + ApplyInTx).
                                                     500 on rating-apply failure leaves the row pending.
POST   /api/v1/game-results/{id}/reject            — rejectGameResult
POST   /api/v1/game-results/{id}/cancel            — cancelGameResult (author only)
GET    /api/v1/players/{tgID}/recent-completed-games — getRecentCompletedGames (query: group_id). Returns past
                                                     games within the result window (RESULT_WINDOW_DAYS, default 14,
                                                     per-group-tz calendar days); ignores the completed flag. No days param.

GET /api/v1/groups/{chatID}/leaderboard            — getGroupLeaderboard
GET /api/v1/players/{tgID}/groups-with-results     — getPlayerGroupsWithResults; returns groups where the
                                                     player has a player_ratings row with games_played > 0
                                                     (not "recent completed games" — that overrepresented
                                                     unrated activity and missed rated players older than 90 d)

GET  /api/v1/audit                                 — listAuditEvents
     required header: X-Caller-Tg-Id (caller's Telegram ID, injected by web service)
     query: limit (default 50, max 200), before_id, event_type, from (RFC3339), to (RFC3339, exclusive upper bound)
     server-owner-only params: group_id, actor_tg_id
     visibility scoping (non-owners):
       - own player events: visibility='player' AND actor_tg_id = caller's TG ID
       - group admin events: visibility IN ('player','group_admin') AND group_id IN (groups where caller is admin)
       both scopes are OR-combined; AdminGroupsResolver calls GetChatAdministrators per group (errors silently ignored)
```

---

## Service layer patterns

### AuditService (`service/audit_service.go`)

Best-effort audit logger. All `Record*` methods call `s.repo.Insert` and silently log errors — they never return errors to callers.

**Event types and visibility:**

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
| `group.bot_added` | server_owner | Bot added to a new group |
| `group.bot_removed` | server_owner | Bot removed from group |
| `group.settings_changed` | group_admin | Admin changes language/timezone |
| `group.changelog_toggled` | server_owner | Admin enables/disables changelog announcements for group |
| `group.auto_booking_allowed_toggled` | server_owner | Server owner enables/disables auto-booking for group; metadata includes cascaded_venue_ids |
| `court.booked` | group_admin | Court booked (scheduler or admin via BookGameCourts) |
| `court.canceled` | group_admin | Scheduler cancels a court |
| `game.published` | group_admin | Game announcement sent to group (by scheduler or admin) |

**Visibility hierarchy:** `server_owner` ≥ `group_admin` ≥ `player`. Server owners (IDs in `SERVICE_ADMIN_IDS` on management/web) see all events. Group admins see their own `player` events plus all `player`+`group_admin` events for groups they administrate. Regular users see only their own `player` events.

**Retention:** `RunRetention(ctx, days)` deletes rows older than `days` days. Called daily by a cron entry in `main.go` (controlled by `AUDIT_RETENTION_DAYS`, default 365).

**Actor propagation pattern:** HTTP handlers receive actor info from the caller:
- POST/PATCH bodies carry `actor_telegram_id` and `actor_display` JSON fields
- DELETE endpoints carry `actor_tg_id` and `actor_display` query params (no body)
- `group_id` is passed as a query param for kick endpoints (handler doesn't know chat ID from path alone)
- Audit is skipped when `actorTgID == 0` (system/anonymous callers)

### GameService / ParticipationService / VenueService

- Constructed in `main.go`, injected into `api.Handler`
- Methods return domain types from `internal/models`
- `ParticipationService` calls `Notifier.EditGameMessage(ctx, gameID)` asynchronously after every join/skip/guest mutation — this fires a Telegram message edit in a background goroutine
- All write operations update the DB then notify; notification failures are logged but do not fail the API response

**`GameService.PublishGame(ctx, gameID, actorTgID int64, actorDisplay string) (*models.Game, error)`**

The canonical publication primitive. Sends the game announcement to the group chat, pins it silently, sets `message_id`, and records a `game.published` audit event.

- Returns `ErrGameNotFound` when `gameRepo.GetByID` returns `pgx.ErrNoRows`; propagates other DB errors as-is.
- Returns `ErrGameAlreadyPublished` when `game.MessageID != nil` — idempotency guard.
- On `api.Send` failure: returns error without touching the DB (game stays cleanly unpublished).
- On `gameRepo.UpdateMessageID` failure: deletes the orphaned Telegram message, then returns error.
- `actorTgID == 0` → audit records `actor_kind = system`; non-zero → `actor_kind = user`.

`NewGameService` signature (13 args): `gameRepo, venueRepo, participationRepo, guestRepo, groupRepo GameRepository…, auditSvc *AuditService, api TelegramAPI, defaultLoc *time.Location, logger *slog.Logger, courtBookingRepo CourtBookingRepository, bookingClient BookingServiceClient, credService *VenueCredentialService, autoBookingResultRepo AutoBookingResultRepository`.

**`GameService.BookGameCourts(ctx, gameID int64, count int, actorTgID int64, actorDisplay string, credCooldown time.Duration) (*BookGameCourtsResult, error)`**

On-demand court booking for an existing game. Delegates to `bookFreeCourts` (shared with `AutoBookingJob`). Appends booked labels to `game.Courts` (multiset-dedup: skips labels already present). Upserts `auto_booking_results` (insert only when no row exists for venue+date+time). Fires `notifier.EditGameMessage` asynchronously using `context.Background()` (detached from request context).

- Returns `ErrGameNotFound` when game is missing.
- Returns `ErrAutoBookingNotAvailable` when venue is nil, `auto_booking_enabled = false`, or `bookingClient`/`credService` is nil.
- Rejects games with time 00:00 (plain error — likely unset game time).
- `BookGameCourtsResult`: `{Requested int, BookedLabels []string, Failures []BookingFailure}`.

**`VenueCredentialService.HasUsableCredentials(ctx, venueID int64, cooldown time.Duration) (ready bool, maxCourts int, err error)`**

Returns `(true, sum-of-MaxCourts, nil)` when at least one credential for the venue is usable (not cooling down). `maxCourts` is the sum of `MaxCourts` across all usable credentials. Returns `(false, 0, nil)` when no credentials exist or all are cooling down. Uses `ListWithPasswordByVenueID` internally and applies the same cooldown logic as `ListForBooking`.

**`VenueService.GetVenueByIDAndGroupID(ctx, id, groupID int64) (*models.Venue, error)`**

Fetches a venue with group ownership enforcement. Used by `bookingReadiness` to prevent cross-group probing.

**`GameService.ListActiveCourtBookings(ctx, gameID int64, courts []string) ([]CourtBookingInfo, error)`**

Returns slim booking info (`CourtBookingInfo{CourtLabel, GameTime, MatchID string}`) for active bookings whose `court_label` matches one of the provided labels. Uses `activeBookingsByLabels` for time-slot scoping. Returns nil/empty when booking infrastructure is absent or the game has no venue.

**`GameService.RemoveCourtsAndCancelBookings(ctx, gameID int64, newCourts string) (canceledLabels []string, cancelErrors []CourtCancelError, err error)`**

Computes a multiset diff of removed courts, looks up active bookings via `activeBookingsByLabels`, cancels each via `BookingServiceClient.CancelMatch` with per-entry credentials (fetched via `VenueCredentialService.GetDecryptedByID`), records audit, then always persists `newCourts` regardless of cancellation failures. Returns `CourtCancelError{CourtLabel, Err}` slices for partial failures. The DB update always runs — partial failures do not block the court list change.

**`GameService.activeBookingsByLabels(ctx, game, labels)` (private)**

Time-slot-aware booking lookup. Calls `autoBookingResultRepo.GetByGameID(game.ID)` to find `game_time`. If non-empty: `GetByVenueAndDateAndTime` + app-level label filter. Otherwise: `GetActiveByVenueDateAndLabels`. Prevents cross-session false matches when a venue hosts multiple sessions on the same date.

### Scheduler (`service/scheduler.go`)

- Runs a single 5-minute cron poll via `robfig/cron/v3`
- `RunScheduledTasks()` → calls `run(false)` on each job in sequence
- Each job is a struct implementing `scheduledJob` interface: `run(force bool)`, `name() string`
- Job names: `cancellation_reminder`, `booking_reminder`, `auto_booking`, `day_after_cleanup`, `auto_approve_results`, `post_leaderboard`

### Scheduled jobs

| Job | File | Window | Dedup guard |
|-----|------|--------|-------------|
| CancellationReminderJob | cancellation_reminder.go | ±2m30s of `game_date - (gracePeriod+6)h` | `notified_day_before` flag |
| BookingReminderJob | booking_reminder.go | [10:00, 10:05) group local time | `last_booking_reminder_at` per venue (date-scoped) |
| AutoBookingJob | auto_booking.go | [00:00, 00:05) group local time | `last_auto_booking_at` per venue (date-scoped) |
| DayAfterCleanupJob | day_after_cleanup.go | [03:00, 03:05) group local time | `completed` flag on game |
| AutoApproveResultsJob | auto_approve_results.go | every poll (48 h cutoff in SQL) | `status = 'pending'` flips to `auto_approved` |
| PostLeaderboardJob | post_leaderboard.go | every poll; gated on 24 h after candidate day's last game start | `bot_groups.last_leaderboard_posted_for` |

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

**BookingReminderJob**: Fires in the `[10:00, 10:05)` window per group timezone for venues with matching `game_days`. Deduplicates via `last_booking_reminder_at` (date-scoped). Injected with `*GameService` to call `PublishGame`.

For venues with `auto_booking_enabled`:
1. `GetByVenueAndDate(venueID, targetDate)` → list of `AutoBookingResult` rows.
2. If empty → DM admins (no booking happened), mark `last_booking_reminder_at`.
3. For each result:
   - `GameID != nil` (game was eagerly created by AutoBookingJob at 00:00): call `gameRepo.GetByID`. On error → fall back to admin DM (not silent). If `game.MessageID != nil` → already published, skip. If `game.MessageID == nil` → call `gameService.PublishGame(ctx, gameID, 0, "")`. On `PublishGame` failure → DM admins. Either way, counts as actioned (single-attempt policy).
   - `GameID == nil` (legacy path — no longer produced after one deploy cycle): create game + announce the old way, call `SetGameID`.
4. Returns true when any slot was actioned → `last_booking_reminder_at` written.

For venues without `auto_booking_enabled` (manual reminder): DM admins a booking reminder (venue name + `booking_opens_days`); mark `last_booking_reminder_at`.

**AutoBookingJob**: Fires in the `[00:00, 00:05)` window per group timezone. Skips groups where `auto_booking_allowed = false` (server-owner toggle). For remaining groups, processes venues with `auto_booking_enabled = true`, `game_days`, and `preferred_game_times` configured. Deduplicates via `last_auto_booking_at`. After a successful booking, immediately creates an **unpublished** game row (`message_id = NULL`) in the DB via `createUnpublishedGame` and calls `SetGameID(resultID, gameID)` to link result → game. The game stays invisible to group members until `BookingReminderJob` calls `PublishGame` at 10:00. If the DB insert fails, DMs group admins (silent) — the auto-booking result still exists and the legacy create-at-10:00 path handles it gracefully.

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

**DayAfterCleanupJob**: Fires in the `[03:00, 03:05)` window. Fetches yesterday's uncompleted games per group (includes unpublished games — no `message_id IS NOT NULL` filter). For games with `message_id != nil`: unpins message and removes keyboard. For games with `message_id == nil` (never announced): skips all Telegram ops, logs silently. Always calls `gameRepo.MarkCompleted` and, if the game has a `venue_id`, `courtBookingRepo.MarkCanceledByVenueAndDate` to bulk-close orphaned active `court_bookings` rows.

**AutoApproveResultsJob**: Runs on every poll. Calls `resultRepo.ListPendingOlderThan(now - 48h)` to find game results that should be auto-approved. For each, opens a single transaction that flips status via `DecideInTx` and applies the Glicko-2 update via `RatingService.ApplyInTx` — so `player_ratings` and `rating_changes` either both land or both roll back with the status flip. Audit + opponent-DM edit + author/opponent DMs run after the commit, best-effort. When `pool == nil` or `ratingSvc == nil` (test wiring / rating disabled), falls back to a plain `Decide` with no rating apply.

**PostLeaderboardJob**: Runs on every poll. Candidate day is yesterday in the group's local timezone (computed from year/month/day, not `time.Truncate`, so the boundary respects `loc`). Three gates before posting:

1. **Already posted**: `bot_groups.last_leaderboard_posted_for >= candidateDate` → skip.
2. **Last game finished ≥ 24 h ago**: queries `gameRepo.GetCompletedGamesByGroupAndDay(chatID, candidateDate, candidateDate+24h)`. If any completed games exist and `time.Now() < max(game_date) + 24h`, return **without marking** so the next poll retries. (The previous version posted as soon as the scheduler ran after local midnight, which was much earlier than 24 h for evening games.)
3. **Any approved results**: filters `gameResultRepo.ListByGroupAndDate` for `approved` / `auto_approved`. If none, mark the day done (terminal no-op) and return.

When all gates pass, builds the leaderboard via `RatingService.GetLeaderboard`, sends a plain-text message (`ParseMode = ""` — player names can contain Markdown control characters and the message has no formatting that needs interpreting), and only then advances `last_leaderboard_posted_for`. A Send failure leaves the marker untouched so the next poll retries.

### Localisation in scheduler jobs

Scheduler jobs resolve language via `groupLang(ctx, groupRepo, chatID)` (calls `GetByID` directly — no HTTP, works without a live Telegram service). Exception: `BookingReminderJob` admin DMs use the admin's Telegram `LanguageCode` via `userLocalizer` since those are personal messages.

### GameNotifier (`service/game_notifier.go`)

Implements `Notifier`. Called asynchronously by `ParticipationService`. Fetches game + participants + guests, formats message and keyboard via `internal/gameformat`, then calls `TelegramAPI.Request(EditMessageTextConfig{})`. Timezone resolved via `resolveGroupTimezone`.

### GameResultService (`service/game_result_service.go`)

Owns the 1-v-1 game-result submission and approval lifecycle. Constructor takes a `*pgxpool.Pool` so it can wrap `Decide` + rating apply in a single transaction; `SetRatingService` is called after construction to break the circular dependency between this service and `RatingService`. Auto-approve cutoff is `autoApproveWindow = 48 * time.Hour` (also reused by `AutoApproveResultsJob`).

| Method | Purpose |
|--------|---------|
| `Submit(ctx, gameID, authorTgID, opponentPlayerID, winnerPlayerID *int64, score, actorDisplay)` | Resolves author from Telegram ID, validates author ≠ opponent, validates both are `registered` in the game, validates score format (`^\d+:\d+$`, winner's side ≥ loser's). Creates a `pending` row; returns it with `AutoApproveAt = SubmittedAt + 48h` populated. Errors: `ErrGameResultNotInGame`, `ErrGameResultBadScore`, `ErrGameResultSamePlayer`, `ErrGameNotFound`. |
| `Get(ctx, id)` | Fetches by ID and populates `Author` / `Opponent` from `playerRepo` (best-effort enrichment, errors ignored). |
| `SetApprovalMessage(ctx, id, chatID, messageID)` | Stores the opponent DM `chat_id` + `message_id` so `AutoApproveResultsJob` can edit the card on timeout. |
| `Approve(ctx, id, opponentTgID, actorDisplay)` | Verifies caller is the opponent. Calls `commitDecision`: when `pool` and `ratingSvc` are wired, opens a tx and runs `resultRepo.DecideInTx` + `ratingSvc.ApplyInTx` together. On any failure the whole tx rolls back and the row stays pending. Records `RecordGameResultApproved` on success (best-effort, outside tx). |
| `Reject(ctx, id, opponentTgID, actorDisplay)` | Verifies caller is the opponent. Plain `Decide` (no rating change). Records `RecordGameResultRejected`. |
| `CancelByAuthor(ctx, id, authorTgID, actorDisplay)` | Verifies caller is the author. Plain `Decide` (no rating change). Records `RecordGameResultCanceled`. |

`commitDecision` falls back to a non-tx `Decide` when `newStatus != Approved`, or when `pool == nil` / `ratingSvc == nil` — used by unit tests and by environments where rating is intentionally disabled.

`storage.ErrGameResultNotPending` is returned (and propagated up as HTTP 409) when the target row was already decided.

### RatingService (`service/rating_service.go`)

Owns Glicko-2 updates and leaderboard queries. Constructor takes the same pool so `Apply` can manage its own transaction. Uses `service/rating/glicko2.go` for the math (`DefaultRating = 1500`, `DefaultRD = 350`, `DefaultVolatility = 0.06`, `Tau = 0.5`, RD clamped to `[30, 350]`).

| Method | Purpose |
|--------|---------|
| `Apply(ctx, result)` | Opens a transaction and delegates to `ApplyInTx`. Used by callers that don't already hold a tx. |
| `ApplyInTx(ctx, tx, result)` | The critical-section update. Locks both `player_ratings` rows `FOR UPDATE` ordered by `player_id ASC` (deadlock prevention). Initialises missing rows with default rating/RD/volatility via `getOrInitForUpdate`. Computes scores `{1, 0.5, 0}` from `WinnerID` (nil → draw). Upserts both ratings and inserts both `rating_changes` rows **inside the caller's tx** — so the leaderboard's current rating and DeltaToday history can never diverge. Audit (`RecordRatingUpdated`) runs after the rating mutation but outside the commit (best-effort). |
| `GetLeaderboard(ctx, groupID)` | Returns `[]LeaderboardEntry{Rank, Player, Rating, RD, GamesPlayed, DeltaToday}` ordered by rating DESC, hiding players with `games_played == 0` and re-numbering ranks after the filter. `DeltaToday` is the sum of `rating_changes.delta` rows in the group's local-tz day `[00:00, 24:00)`. |
| `ListGroupsForPlayer(ctx, playerID)` | Returns group IDs where the player has a `player_ratings` row with `games_played > 0`. Backs `GET /api/v1/players/{tgID}/groups-with-results`. |

`Apply` is now called synchronously from `GameResultService.commitDecision` and `AutoApproveResultsJob.autoApproveOne`; the older async-goroutine path was removed because a crash or DB blip between Decide and Apply could permanently desync the leaderboard.

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
bot_groups:         chat_id PK, title, bot_is_admin, language DEFAULT 'en', timezone DEFAULT 'UTC',
                    changelog_enabled BOOLEAN DEFAULT TRUE, auto_booking_allowed BOOLEAN DEFAULT TRUE,
                    last_leaderboard_posted_for DATE NULL (dedup for PostLeaderboardJob; set to candidate date after a successful send or a confirmed no-results day)
service_state:      key TEXT PK, value TEXT NOT NULL  — generic KV store; used to track `last_changelog_version`
venues:             id, group_id FK→bot_groups, name, courts, time_slots, address,
                    grace_period_hours DEFAULT 24, game_days, booking_opens_days DEFAULT 14,
                    last_booking_reminder_at, preferred_game_times TEXT DEFAULT '', last_auto_booking_at,
                    auto_booking_courts, auto_booking_enabled DEFAULT FALSE,
                    auto_booking_games_count INT DEFAULT 0 (0 = skip booking; N = book at most N slots per run),
                    UNIQUE(group_id, name)
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
game_results:       id BIGSERIAL PK, game_id BIGINT FK→games ON DELETE CASCADE,
                    group_id BIGINT, author_id BIGINT FK→players, opponent_id BIGINT FK→players,
                    winner_id BIGINT NULL FK→players (CHECK: IS NULL OR IN (author_id, opponent_id); NULL = draw),
                    score TEXT DEFAULT '' (format "N:M" or empty),
                    status TEXT CHECK IN ('pending','approved','auto_approved','rejected','canceled'),
                    submitted_at TIMESTAMPTZ DEFAULT NOW(), decided_at TIMESTAMPTZ NULL,
                    approval_chat_id BIGINT NULL, approval_message_id INT NULL (opponent's DM card — set after Submit so the auto-approve job can edit it)
                    CHECK author_id ≠ opponent_id
                    INDEX: (game_id), (group_id, status, decided_at DESC),
                           (status, submitted_at) WHERE status='pending'  — drives ListPendingOlderThan
player_ratings:     PRIMARY KEY (group_id, player_id),
                    rating DOUBLE PRECISION DEFAULT 1500, rd DOUBLE PRECISION DEFAULT 350,
                    volatility DOUBLE PRECISION DEFAULT 0.06, games_played INT DEFAULT 0,
                    updated_at TIMESTAMPTZ DEFAULT NOW()
                    (one row per (group, player); created lazily on first Apply via getOrInitForUpdate)
rating_changes:     id BIGSERIAL PK, game_result_id BIGINT FK→game_results ON DELETE CASCADE,
                    group_id BIGINT, player_id BIGINT,
                    old_rating, new_rating, old_rd, new_rd, delta DOUBLE PRECISION,
                    applied_at TIMESTAMPTZ DEFAULT NOW()
                    INDEX: (group_id, player_id, applied_at DESC), (group_id, applied_at DESC)
                    (one row per player per applied result; powers DeltaToday in GetLeaderboard)
audit_events:       id BIGSERIAL PK, occurred_at TIMESTAMPTZ DEFAULT NOW(),
                    event_type TEXT, visibility TEXT ('player'|'group_admin'|'server_owner'),
                    actor_kind TEXT ('user'|'system'), actor_tg_id BIGINT (nullable),
                    actor_display TEXT, group_id BIGINT (nullable, NO FK — stored value; deleting a group does not null-out historical events),
                    subject_type TEXT, subject_id TEXT, description TEXT, metadata JSONB (nullable)
                    INDEX: (group_id, occurred_at DESC), (actor_tg_id, occurred_at DESC),
                           (event_type, occurred_at DESC)
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
SERVICE_ADMIN_IDS=            optional; comma-separated Telegram user IDs treated as server owners;
                              grants full audit event visibility (server_owner tier) and server-owner flag in web SPA
AUDIT_RETENTION_DAYS=365      optional; how long audit events are kept (daily retention cron deletes older rows)
RESULT_WINDOW_DAYS=14         optional; how far back a past game stays eligible for result submission (per-group-tz calendar days; ignores completed flag)
```

---

## Constraints and conventions

- **Audit any action that meaningfully changes state.** Every new endpoint or service method that creates, updates, or deletes a meaningful entity must call the appropriate `AuditService.Record*` method. Use the table in the AuditService section to pick visibility. Steps:
  1. Add an `AuditEventType` constant in `internal/models/audit_event.go`.
  2. Add a `Record<Action>` method on `AuditService` in `audit_service.go` following the best-effort pattern (call `s.record`, never return error). Set `Visibility` to the lowest tier that should see the event: `player` for self-service actions, `group_admin` for admin-gated actions, `server_owner` for global/infra events.
  3. Call the method from the API handler (after the state-changing operation succeeds) or from the scheduler job. Propagate actor info via the **actor propagation pattern** (body fields for POST/PATCH/PUT; query params `actor_tg_id`, `actor_display`, `group_id` for DELETE). Skip the call when `actorTgID == 0`.
  4. **Mirror the new type in the frontend** — add the string literal to the `AuditEventType` union in `web/frontend/src/types.ts` AND add a human-readable label in `web/frontend/src/auditEvents.ts` (`EVENT_LABELS`). The test `src/auditEvents.test.ts` reads `internal/models/audit_event.go` at test time and fails CI if the two sides diverge — run `npm test` in `web/frontend/` to verify.
  5. Update the event-type table in this SKILL.md.
- New business rules go in `service/`, not `api/`
- HTTP handlers in `api/` only validate input, call service methods, and write responses
- New scheduled logic requires a new job struct in `service/` registered in `scheduler.go`
- `ParticipationService.Notifier` call pattern: always async (`go notifier.EditGameMessage(...)`) so a slow Telegram API never blocks the HTTP response
- `BookingServiceClient` interface in `booking_client.go`: `ListCourts(ctx, date, login, password string)`, `ListMatches(ctx, date, startTime, endTime string, my bool, login, password string)`, `CancelMatch(ctx, matchUUID, login, password string) error`, `BookMatch(ctx, courtUUID, start, end, login, password string)` — `login`/`password` select a per-credential Eversports session on the booking service (forwarded as `X-Eversports-Email`/`-Password` headers); empty strings fall back to the service-level default (env-var) account
- `VenueCredentialService` is injected into `AutoBookingJob`; `credCooldown` (`CREDENTIAL_ERROR_COOLDOWN`, default 24h) gates which credentials are eligible; `MarkError` sets `last_error_at` on failure
- Version is read from `cmd/management/VERSION` file, injected at build time via `-ldflags "-X main.Version=<ver>"`

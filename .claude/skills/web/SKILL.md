---
name: web
description: Architecture reference for the squash-bot web service (cmd/web). Load before planning any changes to the web API, Telegram Login Widget auth, JWT sessions, or React frontend.
user-invocable: true
---

# Web Service — Architecture Reference

The web service serves the React SPA and a small JSON API for authenticated users. Authentication uses the Telegram Login Widget — no username/password. The React frontend is compiled and embedded in the Go binary. No database — all data comes from the management service via HTTP.

**Entry point:** `cmd/web/main.go`  
**Port:** 8082 (env `SERVER_PORT`)  
**Module path:** `github.com/hutoroff/squash-bot/cmd/web`

---

## Package structure

```
cmd/web/
├── main.go              — wiring: embed FS, AuthHandler, GamesHandler, AuditHandler, Handler, Server
└── webserver/
    ├── server.go        — NewServer, Run
    ├── handler.go       — Handler struct, NewHandler, RegisterRoutes, spaFileServer,
    │                      handleConfig, writeJSON, writeError, decodeJSON
    ├── auth.go          — AuthHandler: handleCallback, handleMe, handleLogout,
    │                      verifyTelegramAuth, issueJWT, parseJWT, jwtClaims struct
    ├── games.go         — GamesHandler: handleListGames, handleGetParticipants,
    │                      handleJoinGame, handleSkipGame, handleAddGuest, handleRemoveGuest
    ├── audit.go         — AuditHandler: handleListAuditEvents (JWT-authenticated proxy)
    ├── mgmt_proxy.go    — mgmtClient (embedded by the proxy handlers): proxy, get, claims,
    │                      authorizeGroup, actorFields/actorQuery, decodeWithActor, writeAPIError
    ├── groups.go        — GroupsHandler: handleListGroups, handleGetGroup, patchGroupSetting,
    │                      handleSetGroupAutoBookingAllowed
    ├── venues.go        — VenuesHandler: venue + credential CRUD, all group-scoped via venueScope
    ├── prefs.go         — PrefsHandler: handleGetPreferences, patchPreference

web/
├── embed.go             — //go:embed frontend/dist; var FS embed.FS
└── frontend/            — React + TypeScript SPA (Vite)
    ├── src/
    │   ├── types.ts         — shared TypeScript types
    │   ├── api/             — API client modules
    │   └── components/      — React components + *.test.tsx
    └── vite.config.ts
```

---

## Routes (`webserver/handler.go → RegisterRoutes`)

No auth middleware on the whole mux — individual handlers check the session cookie.

```
GET  /health
GET  /version
GET  /api/config                  — returns {"botName": "<TELEGRAM_BOT_NAME>"}

GET  /api/auth/callback           — Telegram Login Widget redirect target
GET  /api/auth/me                 — returns current user from JWT (200 or 401)
POST /api/auth/logout             — expires session cookie

GET  /api/games                   — list current user's games (requires session)
GET  /api/games/{id}/participants — participants + guests (requires session)
POST /api/games/{id}/join         — join game (requires session)
POST /api/games/{id}/skip         — skip game (requires session)
POST /api/games/{id}/guest        — add guest (requires session)
DELETE /api/games/{id}/guest      — remove guest (requires session)

GET  /api/audit                   — list audit events (requires session); all query params
                                    forwarded to management; caller's TG ID injected via
                                    X-Caller-Tg-Id header; visibility enforced by management

GET  /api/groups                  — groups the caller may manage (all for a server owner, the
                                    ones they administer otherwise, [] for a plain user);
                                    always proxies GET /api/v1/admins/{tgID}/groups
GET  /api/groups/{chatID}         — single group (authorizeGroup)
PATCH /api/groups/{chatID}/language                  — body {language}
PATCH /api/groups/{chatID}/timezone                  — body {timezone}
PATCH /api/groups/{chatID}/changelog                 — body {changelog_enabled}
PATCH /api/groups/{chatID}/leaderboard-notifications — body {leaderboard_notifications_enabled}
                                    (all four: authorizeGroup, actor fields injected from JWT)
PATCH /api/groups/{chatID}/auto-booking-allowed — server-owner only; body {enabled}

GET|POST   /api/groups/{chatID}/venues                       — list / create
GET|PATCH|DELETE /api/groups/{chatID}/venues/{venueID}       — read / full-object update / delete
GET  /api/groups/{chatID}/venues/{venueID}/booking-readiness
GET|POST   /api/groups/{chatID}/venues/{venueID}/credentials
DELETE     /api/groups/{chatID}/venues/{venueID}/credentials/{cid}
GET  /api/groups/{chatID}/venues/{venueID}/credentials/priorities
                                    (all venue routes: authorizeGroup, group_id forced to the
                                    path chatID, actor injected; upstream 400/409/503 proxied verbatim)

GET  /api/me/preferences          — caller's DM preferences (404 from management → frontend defaults)
PATCH /api/me/dm-language         — body {language}
PATCH /api/me/results-opt-out     — body {opt_out}
                                    (Telegram ID always taken from the JWT, never the URL)

GET  /                            — SPA fallback (serves index.html for all unmatched routes)
```

---

## Authentication flow (`webserver/auth.go`)

### Telegram Login Widget

1. Frontend renders the widget using `TELEGRAM_BOT_NAME` from `GET /api/config`
2. User approves in Telegram app → browser redirected to `GET /api/auth/callback?id=...&first_name=...&auth_date=...&hash=...`
3. Backend calls `verifyTelegramAuth`:
   - Builds `check_string` = sorted `key=value` pairs (all params except `hash`), joined by `\n`
   - `secret_key = SHA256(TELEGRAM_BOT_TOKEN)` (raw bytes, not hex)
   - `expected_hash = hex(HMAC-SHA256(secret_key, check_string))`
   - Also verifies `auth_date` is ≤ 86400 seconds old
4. Calls management service `GET /api/v1/players/{telegramID}` to get `player_id` (may be nil if user has never used the bot)
5. Issues HS256 JWT (`JWT_SECRET`, 7-day expiry), sets `session` cookie:
   - HttpOnly, SameSite=Lax
   - `Secure` flag when `r.TLS != nil` OR `X-Forwarded-Proto: https`

### JWT claims (`JWTClaims` struct in auth.go)

```go
type JWTClaims struct {
    TelegramID    int64  `json:"telegram_id"`
    FirstName     string `json:"first_name"`
    LastName      string `json:"last_name,omitempty"`
    Username      string `json:"username,omitempty"`
    PhotoURL      string `json:"photo_url,omitempty"`
    PlayerID      *int64 `json:"player_id,omitempty"`  // nil if not yet a player
    IsServerOwner bool   `json:"so,omitempty"`          // set at login; stale after config change
    Exp           int64  `json:"exp"`
}
```

`GET /api/auth/me` always reads `is_server_owner` from the live `serverOwnerIDs` config map, not from the JWT claim. This means revoking or granting server-owner status takes effect on the next page load without requiring a re-login.

### Player ID lazy lookup

`GET /api/auth/me` and `GET /api/games`: if `PlayerID` is nil in the JWT, a live `lookupPlayer(telegramID)` call is made. If found, the session cookie is refreshed with an updated JWT containing `PlayerID`, so subsequent requests skip the re-lookup.

---

## Games handler (`webserver/games.go`)

**Player ID is always read from the JWT claim**, never from a query parameter. This is a security invariant — do not add player-ID query params.

`handleListGames`:
1. Extracts `playerID` from JWT (or looks up lazily)
2. Calls management `GET /api/v1/players/{playerID}/games`
3. Returns `[]PlayerGame` (newest first)

Mutating handlers (join/skip/guest):
1. Extract `playerID` from JWT
2. Call corresponding management endpoint
3. Management's `ParticipationService` fires `Notifier.EditGameMessage` async → Telegram message updated in place
4. Return the result directly (no second round-trip needed)

`handleGetParticipants`:
- Management `GET /api/v1/games/{id}/participations` + `GET /api/v1/games/{id}/guests`
- Frontend only calls this when the user expands a game card (past games section collapsed by default)

---

## Frontend (`web/frontend/`)

**Build:** `go generate ./web/...` runs `npm ci && npm run build` in `web/frontend/`. Output goes to `web/frontend/dist/`, embedded into the binary via `web/embed.go`.

**Test setup notes (from AGENTS.md):**
- Framework: Vitest + Testing Library
- `globals: true` required in `vite.config.ts` (Testing Library's `afterEach(cleanup)` uses global `afterEach`)
- `vi.mock('../api/games', factory)` keeps `ApiError` class inline with `vi.fn()` stubs so tests can `instanceof`-check it
- Ambiguous text selectors: use `getByRole('heading', { name: '...' })` when text appears in both heading and badge
- Test files excluded from `tsconfig.json` (`"exclude"`) — `tsc && vite build` does not type-check them

**SPA routing:** `spaFileServer` in `handler.go` serves `index.html` for any path not matching a real static file — this enables client-side routing.

---

## Audit handler (`webserver/audit.go`)

`AuditHandler` is a thin JWT-authenticated proxy for `GET /api/v1/audit` on the management service.

1. Reads the caller's `TelegramID` from the JWT session cookie via `auth.claimsFromRequest`. Returns 401 if the session is missing or invalid.
2. Forwards the full query string (limit, before_id, event_type, from, to, group_id, actor_tg_id) to management unchanged.
3. Injects `X-Caller-Tg-Id: <telegramID>` so management can enforce per-user visibility rules.
4. Streams the management response body directly to the client (status code + body proxied verbatim).

Visibility enforcement happens entirely in management — the web service never filters events itself.

---

## Settings proxies (`webserver/mgmt_proxy.go`, `groups.go`, `venues.go`, `prefs.go`)

`mgmtClient` is embedded by `GroupsHandler`, `VenuesHandler` and `PrefsHandler` and carries the
shared auth + management connection, so a new settings handler is a struct embedding it plus routes.

**`authorizeGroup(w, r)` — the single gate for every group-scoped route.** Resolves `{chatID}`, then:
missing/invalid session → 401; server owner → allowed without any lookup; otherwise
`GET /api/v1/admins/{tgID}/groups` and the chat ID must be in the list → else 403. No web-side
cache: the management resolver caches for 5 minutes, and web→management is one local call.

**Security invariants** (mirror the player-ID rule for games):
- `group_id` on venue and credential mutations is **forced to the path chatID**, never read from the body.
- `actor_telegram_id` / `actor_display` always come from the JWT — `decodeWithActor` overwrites whatever the client sent.
- Personal preference routes derive the Telegram ID from the JWT; there is deliberately no `{tgID}` path param.
- `GET /api/v1/venues/{id}` is the one management read that is not group-scoped, so `venueScope`
  fetches the venue first and 404s when `venue.group_id != chatID` before proxying anything.

Upstream status codes and bodies are proxied verbatim (400 `auto_booking_disallowed_by_owner`,
409 duplicate login / active bookings, 503 credentials-disabled), so the frontend maps them to text.

---

## Frontend pages

| Route | Component | Notes |
|---|---|---|
| `/` | `GamesPage` | |
| `/groups` | `GroupsPage` | visible to everyone; empty state for non-admins (no more 403) |
| `/groups/:chatId` | `GroupSettingsPage` | General / Notifications / Auto-booking / Venues; optimistic toggles with rollback; master switch owner-only |
| `/groups/:chatId/venues/new`, `/:venueId` | `VenueFormPage` | full-object save; chip editors for courts, time slots, game days, preferred times; credentials section in edit mode only |
| `/settings` | `SettingsPage` | DM language + results opt-out |
| `/audit` | `AuditPage` | |

Shared bits: `components/Field.tsx` (label + permanent help text — every settings field has one),
`settingsLabels.ts` (`LANGUAGES`, `WEEKDAYS` indexed by Go `time.Weekday`, `READINESS_TEXT`,
`splitList`/`joinList` for the comma-separated storage columns, `scheduleSummary`).
Timezones come from native `Intl.supportedValuesOf('timeZone')` — no hardcoded list, no dependency.

---

## Management service calls made by web service

All with `Authorization: Bearer <INTERNAL_API_SECRET>`.

```
GET  /api/v1/players/{telegramID}         — login: check if player exists, get player_id
GET  /api/v1/players/{playerID}/games     — list user's games (PlayerGame records)
GET  /api/v1/games/{id}/participations    — get participants
GET  /api/v1/games/{id}/guests            — get guests
POST /api/v1/games/{id}/join              — join
POST /api/v1/games/{id}/skip              — skip
POST /api/v1/games/{id}/guests            — add guest
DELETE /api/v1/games/{id}/guests          — remove guest
GET  /api/v1/audit                        — audit event query (with X-Caller-Tg-Id injected)
GET  /api/v1/admins/{tgID}/groups         — groups the caller may manage; backs GET /api/groups
                                            and every authorizeGroup check
GET  /api/v1/groups/{chatID}              — single group
PATCH /api/v1/groups/{chatID}/{language|timezone|changelog|leaderboard-notifications|auto-booking-allowed}
GET|POST /api/v1/venues, GET|PATCH|DELETE /api/v1/venues/{id}
GET  /api/v1/venues/{id}/booking-readiness
GET|POST /api/v1/venues/{id}/credentials, DELETE /api/v1/venues/{id}/credentials/{cid}
GET  /api/v1/venues/{id}/credentials/priorities
GET  /api/v1/users/{tgID}/preferences
PATCH /api/v1/users/{tgID}/{dm-language|results-opt-out}
```

---

## Environment variables

```
TELEGRAM_BOT_TOKEN=          required (verifies Login Widget HMAC)
TELEGRAM_BOT_NAME=           required (bot username without @, for widget config)
MANAGEMENT_SERVICE_URL=      required (e.g. http://management:8080)
INTERNAL_API_SECRET=         required (calls to management service)
JWT_SECRET=                  required (HS256, 7-day expiry; generate: openssl rand -hex 32)
SERVICE_ADMIN_IDS=           optional; comma-separated Telegram user IDs treated as server owners;
                             sets is_server_owner in JWT at login and is re-checked live on every
                             GET /api/auth/me call — changes take effect without re-login
SERVER_PORT=8082             default
LOG_LEVEL=INFO
LOG_DIR=               optional; writes $LOG_DIR/app.log (10 MB / 5 backups, gzip) + stdout
TIMEZONE=UTC
```

**BotFather setup (one-time per deployment):** `/mybots` → Bot Settings → Domain → enter public hostname. `localhost` is not accepted; use ngrok or similar for local end-to-end testing.

---

## Constraints and conventions

- Player ID in API responses/requests **must come from JWT** — never trust client-supplied player IDs for mutations
- `Secure` cookie flag is set based on `r.TLS` OR `X-Forwarded-Proto: https` — both paths must be preserved when changing cookie issuance
- The SPA is embedded at build time — frontend changes require `go generate ./web/...` before building the Go binary
- Adding a new API endpoint: add to `RegisterRoutes`, implement on `GamesHandler` or new handler struct; all game data comes from management service — do not add a DB dependency to the web service
- `GET /api/auth/me` must remain cheap — it's called on every page load; avoid adding slow operations to it
- Version in `cmd/web/VERSION`, injected via `-ldflags "-X main.Version=<ver>"`
- CI verifies both `build-and-test` AND `frontend-test` jobs before the web release workflow proceeds
- **Audit event types:** backend constants live in `internal/models/audit_event.go`; the frontend mirror is `web/frontend/src/auditEvents.ts` (`EVENT_LABELS` + derived `EVENT_TYPE_OPTIONS`) and the `AuditEventType` union in `types.ts`. `auditEvents.test.ts` reads the Go file at test time and fails if the two sides diverge — run `npm test` to verify after adding or renaming backend constants.

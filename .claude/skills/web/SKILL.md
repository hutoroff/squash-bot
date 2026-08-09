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
├── main.go              — wiring: embed FS, AuthHandler, GamesHandler, AuditHandler, GroupsHandler,
│                          VenuesHandler, PrefsHandler, UsersHandler, Handler, Server
└── webserver/
    ├── server.go        — NewServer, Run
    ├── handler.go       — Handler struct, NewHandler, RegisterRoutes, spaFileServer,
    │                      handleConfig, writeJSON, writeError, decodeJSON
    ├── auth.go          — AuthHandler: handleCallback, handleMe, handleLogout, resolveUser, getUser,
    │                      verifyTelegramAuth, issueJWT, parseJWT, JWTClaims struct
    ├── mgmt_proxy.go    — mgmtClient (embedded by the proxy handlers): proxy, get, claims,
    │                      authorizeGroup, actorFields/actorQuery, decodeWithActor, writeAPIError
    ├── games.go         — GamesHandler: handleListGames, handleGetParticipants,
    │                      handleJoinGame, handleSkipGame, handleAddGuest, handleRemoveGuest
    ├── audit.go         — AuditHandler: handleListAuditEvents (JWT-authenticated proxy)
    ├── groups.go        — GroupsHandler: handleListGroups, handleGetGroup, patchGroupSetting,
    │                      handleSetGroupAutoBookingAllowed
    ├── venues.go        — VenuesHandler: venue + credential CRUD, all group-scoped via venueScope
    ├── prefs.go         — PrefsHandler: handleGetPreferences, patchPreference
    └── users.go         — UsersHandler: handleListUsers, handleSetServerOwner (owner-only)

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
GET  /api/config                  — returns {"bot_name": "<TELEGRAM_BOT_NAME>"}

GET  /api/auth/callback           — Telegram Login Widget redirect target
GET  /api/auth/me                 — returns current user from JWT + live is_server_owner (200 or 401)
POST /api/auth/logout             — expires session cookie

GET  /api/games                   — list current user's games (requires session)
GET  /api/games/{id}/participants — participants + guests (requires session)
POST /api/games/{id}/join         — join game (requires session)
POST /api/games/{id}/skip         — skip game (requires session)
POST /api/games/{id}/guest        — add guest (requires session)
DELETE /api/games/{id}/guest      — remove guest (requires session)

GET  /api/audit                   — list audit events (requires session); all query params
                                    forwarded to management; caller's canonical user ID injected via
                                    X-Caller-User-Id header; visibility enforced by management

GET  /api/groups                  — groups the caller may manage (all for a server owner, the
                                    ones they administer otherwise, [] for a plain user);
                                    always proxies GET /api/v1/users/{uid}/admin-groups
GET  /api/groups/{chatID}         — single group (authorizeGroup)
PATCH /api/groups/{chatID}/language                  — body {language}
PATCH /api/groups/{chatID}/timezone                  — body {timezone}
PATCH /api/groups/{chatID}/changelog                 — body {changelog_enabled}
PATCH /api/groups/{chatID}/leaderboard-notifications — body {leaderboard_notifications_enabled}
                                    (all four: authorizeGroup, actor fields injected from JWT)
PATCH /api/groups/{chatID}/auto-booking-allowed — server-owner only; body {enabled}; no local
                                    pre-check — management enforces the owner check itself and its
                                    403 is proxied verbatim

GET|POST   /api/groups/{chatID}/venues                       — list / create
GET|PATCH|DELETE /api/groups/{chatID}/venues/{venueID}       — read / full-object update / delete
GET  /api/groups/{chatID}/venues/{venueID}/booking-readiness
GET|POST   /api/groups/{chatID}/venues/{venueID}/credentials
DELETE     /api/groups/{chatID}/venues/{venueID}/credentials/{cid}
GET  /api/groups/{chatID}/venues/{venueID}/credentials/priorities
                                    (all venue routes: authorizeGroup, group_id forced to the
                                    path chatID, actor injected; upstream 400/409/503 proxied verbatim)

GET  /api/me/preferences          — caller's full user record (display_name, is_server_owner,
                                    dm_language, results_opt_out) — proxies GET /api/v1/users/{uid};
                                    no 404 case, the user always exists once resolved at login
PATCH /api/me/dm-language         — body {language}
PATCH /api/me/results-opt-out     — body {opt_out}
                                    (the canonical user ID always comes from the JWT, never the URL)

GET  /api/users                   — owner-only user list (display_name, providers, created_at,
                                    is_server_owner); injects X-Caller-User-Id, management enforces
                                    the owner check against the DB
PATCH /api/users/{userID}/server-owner — grant/revoke the server-owner role; body {enabled};
                                    actor forced from the JWT via decodeWithActor; 403 if the actor
                                    isn't already an owner, 409 (ErrLastServerOwner) proxied verbatim
                                    when asked to revoke the last remaining owner

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
4. Calls management's `POST /api/v1/identities/resolve` (`resolveUser`) with `{provider: "telegram", external_id: <tgID>, username, first_name, last_name, photo_url}` to find-or-create the canonical user and get `{user_id, player_id, display_name, is_server_owner}`. On any resolve failure (management unreachable, 5xx), `handleCallback` returns 500 and issues **no** session cookie — it never falls back to a fabricated identity.
5. Issues HS256 JWT (`JWT_SECRET`, 7-day expiry) carrying the resolved `user_id` (never the raw Telegram ID), sets `session` cookie:
   - HttpOnly, SameSite=Lax
   - `Secure` flag when `r.TLS != nil` OR `X-Forwarded-Proto: https`

### JWT claims v2 (`JWTClaims` struct in auth.go)

```go
type JWTClaims struct {
    UserID    int64  `json:"uid"`
    PlayerID  *int64 `json:"pid,omitempty"`  // nil until the user's first game join
    FirstName string `json:"fn"`
    LastName  string `json:"ln,omitempty"`
    Username  string `json:"un,omitempty"`
    PhotoURL  string `json:"ph,omitempty"`
    Exp       int64  `json:"exp"`
}
```

There is deliberately no `is_server_owner` claim — see below. `parseJWT` rejects any token where `UserID == 0`, which deterministically kills every pre-migration session cookie (the old `JWTClaims` shape never had a `uid` field, so it unmarshals to zero) — this forces a clean re-login rather than silently resolving to a fake user 0.

`GET /api/auth/me` always calls `GET /api/v1/users/{uid}` (`getUser`) and returns its live `is_server_owner`, never a JWT claim. A role change made on the Users page therefore takes effect on the very next `/api/auth/me` call, with no re-login and no stale-JWT window to reason about.

### No player-ID lazy lookup

Unlike the pre-refactor version, there is **no** `lookupPlayer`/`resolvePlayerID` machinery. The user always exists once resolved (`POST /api/v1/identities/resolve` creates it), so every route that used to special-case "player doesn't exist yet" (`GET /api/me/preferences`, `GET /api/games`) now simply proxies to a `user_id`-scoped management route and gets back an empty/default value instead of a 404.

---

## Games handler (`webserver/games.go`)

**The canonical `user_id` is always read from the JWT claim**, never from a query parameter or the request body. This is a security invariant — do not add a `user_id` query param that a client could set.

`handleListGames`:
1. Extracts `claims.UserID` from the JWT
2. Calls management `GET /api/v1/users/{userID}/games` — returns `[]` for a user who has never joined a game, no special-casing needed on this side
3. Returns `[]PlayerGame` (newest first)

Mutating handlers (join/skip/guest):
1. Extract `claims.UserID` from the JWT
2. `authorizeGameAccess` calls management `GET /api/v1/games/{id}/access?user_id=<uid>` first — the IDOR guard; 403 without calling the action endpoint if the user isn't associated with the game's group
3. Call the corresponding management endpoint with body `{"user_id": claims.UserID}` (join/skip/guest-add) or `{"user_id": claims.UserID}` (guest-remove) — no profile fields; management's resolve step already owns the profile
4. Management's `ParticipationService` fires `Notifier.EditGameMessage` async → Telegram message updated in place
5. Return the result directly (no second round-trip needed)

`handleGetParticipants`:
- Management `GET /api/v1/games/{id}/participations` + `GET /api/v1/games/{id}/guests`
- Each participant/guest's `player` object carries `user_id` (needed by the frontend to detect "is this me" without a Telegram ID — see below) alongside the existing `telegram_id`/`username`/`first_name`/`last_name` display fields. `mgmtPlayer` in games.go must declare `user_id` explicitly or it is silently dropped during the decode→re-encode proxy step.
- Frontend only calls this when the user expands a game card (past games section collapsed by default)

---

## Frontend (`web/frontend/`)

**Build:** `go generate ./web/...` runs `npm ci && npm run build` in `web/frontend/`. Output goes to `web/frontend/dist/`, embedded into the binary via `web/embed.go`.

**Identity types (`types.ts`):** `User` (from `/api/auth/me`) is `{user_id, first_name, last_name?, username?, photo_url?, is_server_owner?}` — no `telegram_id`, no `player_id`. `GamePlayer` (participants/guests inside a game) keeps `telegram_id` for display but also carries a required `user_id`, which `GameCard.tsx` uses to determine whether a given participation/guest belongs to the signed-in user (`p.player.user_id === user.user_id`) — comparing by Telegram ID is no longer possible since `User` doesn't have one. `AdminUser` (Users page rows) is `{user_id, display_name, is_server_owner, dm_language, results_opt_out, created_at, providers}`.

**`App.tsx` auth bootstrap:** `GET /api/auth/me` is fetched once on mount. A `401` response means truly unauthenticated → show `<Login>`. Any other failure (502 from an unreachable management service, a network error) sets a distinct `authError` state and shows a retry prompt instead — it must **not** be treated as "logged out", or a transient upstream outage would bounce every signed-in user to the login screen.

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

1. Reads the caller's `UserID` from the JWT session cookie via `auth.claimsFromRequest`. Returns 401 if the session is missing or invalid.
2. Forwards the full query string (limit, before_id, event_type, from, to, group_id, actor_user_id) to management unchanged.
3. Injects `X-Caller-User-Id: <userID>` so management can enforce per-user visibility rules.
4. Streams the management response body directly to the client (status code + body proxied verbatim).

Visibility enforcement happens entirely in management — the web service never filters events itself.

---

## Settings proxies (`webserver/mgmt_proxy.go`, `groups.go`, `venues.go`, `prefs.go`, `users.go`)

`mgmtClient` is embedded by `GroupsHandler`, `VenuesHandler`, `PrefsHandler`, and `UsersHandler` and carries the
shared auth + management connection, so a new settings handler is a struct embedding it plus routes.

**`authorizeGroup(w, r)` — the single gate for every group-scoped route.** Resolves `{chatID}`, then always calls
`GET /api/v1/users/{uid}/admin-groups` and requires the chat ID to be in the returned list → else 403. There is
**no local server-owner shortcut** — management's `listAdminGroups` already returns every group when the caller
is an owner, so trusting that one lookup is sufficient and keeps web from duplicating an authorization decision
that only the DB can make correctly.

**Security invariants** (mirror the user-ID rule for games):
- `group_id` on venue and credential mutations is **forced to the path chatID**, never read from the body.
- `actor_user_id` / `actor_display` always come from the JWT — `decodeWithActor` overwrites whatever the client sent, for every handler that calls it (groups, venues, users).
- Personal preference routes derive the user ID from the JWT; there is deliberately no `{userID}` path param.
- `GET /api/v1/venues/{id}` is the one management read that is not group-scoped, so `venueScope`
  fetches the venue first and 404s when `venue.group_id != chatID` before proxying anything.
- `handleSetGroupAutoBookingAllowed` and `UsersHandler.handleSetServerOwner` have **no local owner
  pre-check** — they forward straight to management, which re-verifies the actor's `is_server_owner`
  against the DB and returns 403 itself. Web never grants access on its own for an owner-only route.

Upstream status codes and bodies are proxied verbatim (400 `auto_booking_disallowed_by_owner`,
403 forbidden, 409 duplicate login / active bookings / `ErrLastServerOwner`, 503 credentials-disabled),
so the frontend maps them to text.

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
| `/users` | `UsersPage` | server-owner only (route conditionally rendered in `App.tsx`, nav link conditionally rendered in `Dashboard.tsx`, both gated on `user.is_server_owner`); lists every user with an owner-role checkbox; a 409 from `setServerOwner` shows "Cannot remove the last server owner." and rolls back the optimistic toggle; a `Set<userID>` (not a single ID) tracks in-flight saves so toggling two different rows concurrently can't let one request's completion re-enable a checkbox whose own save is still pending |

Shared bits: `components/Field.tsx` (label + permanent help text — every settings field has one),
`settingsLabels.ts` (`LANGUAGES`, `WEEKDAYS` indexed by Go `time.Weekday`, `READINESS_TEXT`,
`splitList`/`joinList` for the comma-separated storage columns, `scheduleSummary`).
Timezones come from native `Intl.supportedValuesOf('timeZone')` — no hardcoded list, no dependency.

---

## Management service calls made by web service

All with `Authorization: Bearer <INTERNAL_API_SECRET>`.

```
POST /api/v1/identities/resolve           — login: find-or-create the canonical user for a Telegram ID
GET  /api/v1/users/{userID}               — handleMe's live is_server_owner; GET /api/me/preferences
GET  /api/v1/users/{userID}/games         — list user's games (PlayerGame records)
GET  /api/v1/games/{id}/access            — per-game IDOR guard (query: user_id)
GET  /api/v1/games/{id}/participations    — get participants
GET  /api/v1/games/{id}/guests            — get guests
POST /api/v1/games/{id}/join              — join
POST /api/v1/games/{id}/skip              — skip
POST /api/v1/games/{id}/guests            — add guest
DELETE /api/v1/games/{id}/guests          — remove guest
GET  /api/v1/audit                        — audit event query (with X-Caller-User-Id injected)
GET  /api/v1/users/{userID}/admin-groups  — groups the caller may manage; backs GET /api/groups
                                            and every authorizeGroup check
GET  /api/v1/groups/{chatID}              — single group
PATCH /api/v1/groups/{chatID}/{language|timezone|changelog|leaderboard-notifications|auto-booking-allowed}
GET|POST /api/v1/venues, GET|PATCH|DELETE /api/v1/venues/{id}
GET  /api/v1/venues/{id}/booking-readiness
GET|POST /api/v1/venues/{id}/credentials, DELETE /api/v1/venues/{id}/credentials/{cid}
GET  /api/v1/venues/{id}/credentials/priorities
PATCH /api/v1/users/{userID}/{dm-language|results-opt-out}
GET  /api/v1/users                        — owner-only list (X-Caller-User-Id header)
PATCH /api/v1/users/{userID}/server-owner — grant/revoke role
```

---

## Environment variables

```
TELEGRAM_BOT_TOKEN=          required (verifies Login Widget HMAC)
TELEGRAM_BOT_NAME=           required (bot username without @, for widget config)
MANAGEMENT_SERVICE_URL=      required (e.g. http://management:8080)
INTERNAL_API_SECRET=         required (calls to management service)
JWT_SECRET=                  required (HS256, 7-day expiry; claim is the canonical uid, never a
                             Telegram ID; generate: openssl rand -hex 32)
SERVER_PORT=8082             default
LOG_LEVEL=INFO
LOG_DIR=               optional; writes $LOG_DIR/app.log (10 MB / 5 backups, gzip) + stdout
TIMEZONE=UTC
```

`SERVICE_ADMIN_IDS` is **not** a web config var — it only exists on the management service (grant-only
bootstrap seed for `is_server_owner`, applied at startup). `WebConfig` has no such field; do not add
one back without re-reading why it was removed (server-owner authority must live in exactly one
place — the DB — enforced by exactly one service — management).

**BotFather setup (one-time per deployment):** `/mybots` → Bot Settings → Domain → enter public hostname. `localhost` is not accepted; use ngrok or similar for local end-to-end testing.

---

## Constraints and conventions

- The canonical `user_id` in API responses/requests **must come from the JWT** — never trust a client-supplied user ID for mutations. The same rule that used to apply to `player_id` now applies to `user_id`.
- `is_server_owner` is **never** cached in the JWT — always fetched live from `GET /api/v1/users/{uid}` in `handleMe`, and the frontend's `User.is_server_owner` is only as fresh as the last `/api/auth/me` call (a page reload after a role change is enough to see it).
- `Secure` cookie flag is set based on `r.TLS` OR `X-Forwarded-Proto: https` — both paths must be preserved when changing cookie issuance.
- The SPA is embedded at build time — frontend changes require `go generate ./web/...` before building the Go binary.
- Adding a new API endpoint: add to `RegisterRoutes`, implement on the relevant handler struct (embed `mgmtClient` if it's a simple authorize-then-proxy pattern); all game/group/user data comes from the management service — do not add a DB dependency to the web service.
- `GET /api/auth/me` must remain cheap — it's called on every page load; avoid adding slow operations to it. It already makes one extra call to management (`getUser`) beyond parsing the JWT — do not add a second one without a measured reason.
- Version in `cmd/web/VERSION`, injected via `-ldflags "-X main.Version=<ver>"`.
- CI verifies both `build-and-test` AND `frontend-test` jobs before the web release workflow proceeds.
- **Audit event types:** backend constants live in `internal/models/audit_event.go`; the frontend mirror is `web/frontend/src/auditEvents.ts` (`EVENT_LABELS` + derived `EVENT_TYPE_OPTIONS`) and the `AuditEventType` union in `types.ts`. `auditEvents.test.ts` reads the Go file at test time and fails if the two sides diverge — run `npm test` to verify after adding or renaming backend constants.
- **Owner-only routes have no local shortcut.** `authorizeGroup`, `handleSetGroupAutoBookingAllowed`, and `UsersHandler` all forward the caller's identity and let management's own DB check decide; adding a `serverOwnerIDs`-style local map back in would reintroduce the exact bug this refactor removed it to fix (a stale/unsynced local list granting authority the DB doesn't agree with).

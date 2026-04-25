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

### JWT claims (`jwtClaims` struct in auth.go)

```go
type jwtClaims struct {
    TelegramID int64  `json:"telegram_id"`
    FirstName  string `json:"first_name"`
    Username   string `json:"username,omitempty"`
    PlayerID   *int64 `json:"player_id,omitempty"`  // nil if not yet a player
    jwt.RegisteredClaims
}
```

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
```

---

## Environment variables

```
TELEGRAM_BOT_TOKEN=          required (verifies Login Widget HMAC)
TELEGRAM_BOT_NAME=           required (bot username without @, for widget config)
MANAGEMENT_SERVICE_URL=      required (e.g. http://management:8080)
INTERNAL_API_SECRET=         required (calls to management service)
JWT_SECRET=                  required (HS256, 7-day expiry; generate: openssl rand -hex 32)
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

---
name: booking
description: Architecture reference for the squash-bot booking service (cmd/booking). Load before planning any changes to the Eversports client, checkout flow, or booking HTTP API.
user-invocable: true
---

# Booking Service — Architecture Reference

The booking service wraps the reverse-engineered Eversports.de internal API. It is a stateless HTTP service (except for the in-memory cookie jar holding the session). No database — all state is transient. The management service calls it for court booking and cancellation.

**Entry point:** `cmd/booking/main.go`  
**Port:** 8081 (env `SERVER_PORT`)  
**Module path:** `github.com/hutoroff/squash-bot/cmd/booking`

---

## Package structure

```
cmd/booking/
├── main.go                  — wiring: eversports.New, booking.NewHandler, booking.NewServer, Run
├── booking/
│   ├── server.go            — NewServer, requireBearer, Run, writeJSON, writeError
│   └── handler.go           — eversportsClient interface, Handler struct, NewHandler,
│                              RegisterRoutes, all 6 HTTP handlers + helpers
└── eversports/
    ├── client.go            — Client struct, New, Login, EnsureLoggedIn, invalidateSession,
    │                          doAuthed, hasCookie, setBrowserHeaders, withAuth generic helper
    ├── matches.go           — GetMatchByID, CancelMatch + wire types
    ├── slots.go             — GetCourts, GetSlots, parseCalendarHTML + wire types
    ├── checkout.go          — CreateBooking, createMatchFromBooking, reportMPFee,
    │                          trackCheckoutCompleted, isCFChallenge + wire types
    ├── facility.go          — GetFacility + wire types
    └── models.go            — Public domain types + shared GQL types + parseTime
```

---

## Eversports client (`eversports/`)

### Client struct (`client.go`)

```go
type Client struct {
    http      *http.Client       // has CookieJar holding the 'et' session cookie
    email     string
    password  string
    loginMu   sync.Mutex         // serialises Login calls
    loggedIn  atomic.Bool
    userID    atomic.Value       // string GraphQL UUID
    bookingMu sync.Mutex         // serialises CreateBooking (see checkout.go)
    logger    *slog.Logger
}
```

Auth is cookie-based: Eversports sets an `et` cookie (UUID, 30-day, httpOnly, SameSite=None) on successful login. The `http.Client`'s `CookieJar` stores it automatically and sends it on every request.

### `withAuth` generic helper (`client.go`)

```go
func withAuth[T any](ctx context.Context, c *Client, do func() (T, error)) (T, error)
```

Used by `GetMatchByID`, `GetCourts`, `GetSlots`, `CancelMatch`, `GetFacility`. Pattern:
1. `EnsureLoggedIn(ctx)` — no-op if already logged in (atomic check, mutex on first call)
2. Call `do()`
3. If `errUnauthorized` → `invalidateSession()` → `EnsureLoggedIn(ctx)` → retry `do()` once

**`CreateBooking` does NOT use `withAuth`** — it has custom mid-flow 401 handling. A 401 after step 1 of the checkout returns an error without retry to avoid duplicate bookings.

### Public methods

| Method | File | Description |
|--------|------|-------------|
| `GetMatchByID(ctx, matchID)` | matches.go | Single booking via GraphQL `Match` query |
| `CancelMatch(ctx, matchID)` | matches.go | GraphQL `CancelMatch` mutation |
| `GetCourts(ctx, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date)` | slots.go | POST to `/api/booking/calendar/update`, parses HTML; dedups by court ID |
| `GetSlots(ctx, facilityID, courtIDs, startDate)` | slots.go | GET `/api/slot` with query params |
| `CreateBooking(ctx, facilityUUID, courtUUID, sportUUID, start, end)` | checkout.go | 5-step checkout; serialised by `bookingMu` |
| `GetFacility(ctx, slug)` | facility.go | GraphQL `VenueProfileVenueContext` query |

### CreateBooking 5-step flow (`checkout.go`)

Serialised via `c.bookingMu` — concurrent calls queue rather than interleave.

```
Step 1: POST /checkout/api/payableitem/courtbooking  — reserve slot
        ↑ Only step that propagates errUnauthorized for retry
Step 2: POST /checkout/api/payment/{id}/pay-offline  — settle payment
Step 3: POST /checkout/api/match/create-from-booking — attach match record (best-effort)
Step 4: POST /checkout/api/tracking/getMPFeeForCourtBooking (best-effort)
Step 5: POST /checkout/api/tracking/trackCheckoutCompleted  (best-effort)
```

Steps 3–5 never abort the booking on failure — errors are logged, result is returned.

### HTML parsing for GetCourts

`parseCalendarHTML` in `slots.go` uses 4 package-level regex vars:
- `calendarCourtRowRe` — `<tr class="court">` rows
- `calendarCourtNameRe` — court display name
- `calendarCourtIDRe` — `data-court="..."` numeric ID
- `calendarCourtUUIDRe` — `data-court-uuid="..."` UUID

### Public domain types (`models.go`)

```
Booking, Court, Slot, SlotMatch, BookingResult, CancellationResult
Facility + FacilityTag, FacilityContact, FacilitySport, FacilityCity, FacilityCompany,
           FacilityVenueRef, FacilityVenueLocation
```

Shared internal types also in models.go: `gqlRequest`, `gqlLoginResponse`, `parseTime`

Operation-private wire types live in their respective files (not exported).

---

## HTTP API (`booking/handler.go`)

### eversportsClient interface (defined in handler.go, not the eversports package)

```go
type eversportsClient interface {
    GetMatchByID(ctx context.Context, matchID string) (*eversports.Booking, error)
    CancelMatch(ctx context.Context, matchID string) (*eversports.CancellationResult, error)
    CreateBooking(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*eversports.BookingResult, error)
    GetSlots(ctx context.Context, facilityID string, courtIDs []string, startDate string) ([]eversports.Slot, error)
    GetCourts(ctx context.Context, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]eversports.Court, error)
    GetFacility(ctx context.Context, slug string) (*eversports.Facility, error)
}
```

`NewHandler(es eversportsClient, ...)` — accepts the interface, not `*eversports.Client`. `main.go` passes `*eversports.Client` which satisfies structurally.

### Routes

All routes except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.

```
GET    /health
GET    /version
GET    /api/v1/eversports/matches?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true|false]
POST   /api/v1/eversports/matches           body: {courtUuid, start, end} (RFC3339)
GET    /api/v1/eversports/matches/{id}
DELETE /api/v1/eversports/matches/{id}
GET    /api/v1/eversports/courts[?date=YYYY-MM-DD]
GET    /api/v1/eversports/facility?slug=<slug>
```

### Per-credential client dispatch (`handler.go`)

`Handler` holds a `credClients sync.Map` (keyed by email → `*eversports.Client`). `getOrCreateCredClient(email, password)` lazily creates and caches a dedicated client per account. `clientFromRequest(r)` reads `X-Eversports-Email`/`X-Eversports-Password` headers and returns the matching cached client, or `h.eversports` if headers are absent.

All handlers support per-credential dispatch; they route to the matching cached client when credentials are present, otherwise fall back to `h.eversports` (service-level default, env-var credentials):

| Handler | Credential source |
|---------|------------------|
| `getMatch` (GET /matches/{id}) | `X-Eversports-Email`/`-Password` headers |
| `cancelMatch` (DELETE /matches/{id}) | JSON body `email`/`password` |
| `createMatch` (POST /matches) | JSON body `email`/`password` |
| `getCourts` (GET /courts) | `X-Eversports-Email`/`-Password` headers |
| `listMatches` (GET /matches) | `X-Eversports-Email`/`-Password` headers (applied to both GetCourts + GetSlots) |
| `getFacility` (GET /facility) | `X-Eversports-Email`/`-Password` headers |

### listMatches handler details

Resolves courts dynamically on each call, using the per-credential client when headers are present:
1. `GetCourts(...)` → court list for the date
2. `GetSlots(facilityID, courtIDs, date)` → all slots
3. `filterSlots(slots, date, startTime, endTime, myFilter)` — pure function, lexicographic HHMM comparison
4. Enriches each slot with `courtUuid` from the ID→UUID map

Squash sport constants are hardcoded in handler.go:
```go
squashSportUUID = "b388b6e6-69de-11e8-bdc6-02bd505aa7b2"
squashSportSlug = "squash"
squashSportName = "Squash"
squashSportID   = "496"
```

---

## Environment variables

```
EVERSPORTS_EMAIL=            required
EVERSPORTS_PASSWORD=         required
INTERNAL_API_SECRET=         required (bearer token for callers)
SERVER_PORT=8081             default
EVERSPORTS_FACILITY_ID=      required for /matches, /courts
EVERSPORTS_FACILITY_UUID=    required for POST /matches
EVERSPORTS_FACILITY_SLUG=    required for /matches, /courts
LOG_LEVEL=INFO
TIMEZONE=UTC
```

---

## Constraints and conventions

- **Booking timezone**: `CreateBooking` `start`/`end` must carry the facility's **local timezone offset** — Eversports rejects UTC (`Z`) timestamps. Callers must pass times in the group's local timezone; do NOT call `.UTC()` before passing (e.g. use `parsePreferredTime(..., groupTZ)` directly).
- `bookingMu` serialises `CreateBooking` — never add concurrent booking logic without understanding the step-3 implicit server state issue
- `withAuth` is for ALL retry logic except `CreateBooking` — do not duplicate the retry pattern manually in new methods
- Adding a new Eversports operation: create/choose the right operation file (`matches.go`, `slots.go`, etc.), put public domain types in `models.go`, put wire types in the operation file, use `withAuth` unless mid-flow 401 is unsafe
- Adding a new HTTP endpoint: add the route in `handler.go → RegisterRoutes`, add the method to `eversportsClient` interface if it calls a new eversports method, implement the handler method on `*Handler`
- `isCFChallenge` detects Cloudflare bot-challenge pages that return HTTP 200 with HTML — always check for it when parsing JSON from Eversports endpoints that may be unprotected
- Version in `cmd/booking/VERSION`, injected via `-ldflags "-X main.Version=<ver>"`

# Sports Booking Service

**booking** is an internal HTTP service (business port 8081, admin port 9091 by default) that connects to [Eversports](https://www.eversports.de/) using credentials supplied per request. Reliable v2 mutations are admitted to a booking-owned PostgreSQL ledger before provider work; sessions and credentials remain memory-only. There is no service-level default account. The service is single-active and refuses startup when its database, identity-key continuity or singleton authority is unavailable. For execution constraints, see [Booking internals](services/booking.md). Production routing, shadow registration and static keyed-v2 rollback are **unexecuted** operator steps in the [discovery rollout handoff](service-discovery-rollout-runbook.md); management can call this service by a reviewed static v2 URL or exact registry binding only after independent [booking cutover gates](booking-reliability-runbook.md).

## Environment Variables

| Variable | Required | Default | Description |
|---|---:|---:|---|
| `BOOKING_DATABASE_URL` | Yes | — | Booking's direct PostgreSQL connection. It runs only the `booking_*` migration stream. |
| `BOOKING_IDENTITY_KEY` | Yes | — | Exactly 32 random bytes as 64 hex characters. Changing it against an existing ledger fails startup. |
| `BOOKING_SERVICE_KEY` | Yes | — | Stable logical key for the configured provider/facility/sport pool. |
| `BOOKING_MONITORING_SECRET` | Yes | — | Admin-listener bearer, at least 32 characters and distinct from `INTERNAL_API_SECRET`. |
| `BOOKING_RELIABLE_CUTOVER_ACK` | Yes | — | Must equal `keyed-v2-only`. Leave unset until management v2 compatibility and the [cutover runbook](booking-reliability-runbook.md) gates pass. It acknowledges semantics; it does not prove compatibility. |
| `EVERSPORTS_FACILITY_ID` / `UUID` / `SLUG` | Yes | — | Explicit trusted facility scope; there is no production facility default. |
| `EVERSPORTS_SQUASH_SPORT_UUID` | Yes | — | Eversports squash UUID used for reliable execution and verified import. |
| `INTERNAL_API_SECRET` | Yes | — | Business API bearer. Monitoring credentials cannot use business routes. |
| `SERVER_PORT` / `BOOKING_ADMIN_PORT` | No | `8081` / `9091` | Separate business and internal admin listeners. Do not publish the admin port. |
| `BOOKING_HTTP_RESPONSE_WAIT` | No | `20s` | Wait for terminal completion before returning `202`. |
| `BOOKING_EXECUTION_TIMEOUT` / `BOOKING_PROVIDER_CALL_TIMEOUT` | No | `90s` / `20s` | Service-owned total execution and per-provider-request bounds. |
| `BOOKING_FINALIZATION_TIMEOUT` / `BOOKING_ADMISSION_WAIT` | No | `5s` / `1s` | DB-only finalization and admission capacity bounds. |
| `BOOKING_SHUTDOWN_TIMEOUT` | No | `100s` | Shared listener/drain/resource-close budget; must cover execution plus finalization. |
| `BOOKING_RETENTION_INTERVAL` / `BOOKING_RETENTION_AGE` / `BOOKING_RETENTION_BATCH_SIZE` | No | `24h` / `2160h` / `100` | Bounded physical result-detail compaction schedule. |
| `BOOKING_SESSION_CAPACITY` / `BOOKING_SESSION_IDLE_TIMEOUT` | No | `32` / `10m` | Disposable in-memory session-cache bounds. |
| `LOG_LEVEL` / `LOG_DIR` / `TIMEZONE` | No | `INFO` / empty / `UTC` | Logging and process timezone settings. |

## API Endpoints

On the business listener, only `/health` is unauthenticated. `/version`, reads and all mutations require `Authorization: Bearer <INTERNAL_API_SECRET>`. On the internal admin listener, `/health/live`, `/health/ready`, `/version`, `/descriptor` and `/metrics` all require `Authorization: Bearer <BOOKING_MONITORING_SECRET>`.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET`  | `/health` | No | Compatibility liveness probe only |
| `GET`  | `/version` | Business | Legacy version response; prefer the admin endpoint for monitoring |
| `POST` | `/api/v2/eversports/matches` | Business + `Idempotency-Key` | Durably admit a create operation; returns terminal status or `202` with status URL |
| `DELETE` | `/api/v2/eversports/matches/{id}` | Business + `Idempotency-Key` | Durably cancel an exact known/imported owned match |
| `GET` | `/api/v2/eversports/operations/{id}` | Business | Sanitized durable status; no provider credentials required |
| `POST` | `/api/v2/eversports/operations/{id}/reconcile` | Business | Fresh credentialed exact-match read for an unknown outcome |
| `POST` | `/api/v2/eversports/operations/{id}/resolution` | Business + `Idempotency-Key` | Keyed, revision-checked, credential-verified resolution |
| `POST` | `/api/v2/eversports/imports` | Business + `Idempotency-Key` | Verify and import a legacy provider booking |
| `POST` / `DELETE` | v1 mutation routes | Business | Rejected with `booking_contract_upgrade_required`; keyless mutation is not a fallback |
| `GET`  | `/api/v1/eversports/matches?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Yes | Court bookings for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means the slot is already reserved. Optionally filter by time window (inclusive) and/or by whether the authenticated user owns the reservation (`my=true\|false`). Court IDs are resolved automatically via `/courts`. Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `POST` | `/api/v1/eversports/matches` | Yes | Rejected with `booking_contract_upgrade_required`; use keyed v2 admission. |
| `GET`  | `/api/v1/eversports/matches/{id}` | Yes | Fetch a single booking by its **match UUID** (the `matchId` returned by `POST /matches`) |
| `DELETE` | `/api/v1/eversports/matches/{id}` | Yes | Rejected with `booking_contract_upgrade_required`; use keyed v2 cancellation. |
| `GET`  | `/api/v1/eversports/courts[?date=YYYY-MM-DD]` | Yes | List courts at the facility; returns `[{"id":"…","uuid":"…","name":"…"}]`. Parses the Eversports booking calendar HTML. Optional `date` parameter (default: today). Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `GET`  | `/api/v1/eversports/facility?slug=<slug>` | Yes | Venue profile for a facility slug; returns `{"id","slug","name","rating","reviewCount","address","hideAddress","tags","contact","sports","city","company"}`. `slug` query parameter is mandatory (400 if missing). Returns 404 if the slug is not found on Eversports. |

## Authentication

Credentials are supplied **per-request** by the management service — either via `X-Eversports-Email`/`X-Eversports-Password` headers (GET endpoints) or as `email`/`password` fields in the JSON body (POST/DELETE endpoints). All six Eversports endpoints return HTTP 400 if credentials are absent.

Reliable mutation execution coordinates by a provider-scoped HMAC of normalized login, serializes all credential versions for that account, and uses a bounded disposable session cache. Password rotation selects fresh session material without changing durable operation replay identity. Fresh reconciliation/import sessions are never inserted into the cache. The legacy read handler retains its process-local per-credential clients until its remaining read routes are migrated.

A terminal unknown create is **not** a failed booking and cannot be automatically replaced. Management's separate owner-only manual duplicate-risk workflow reconciles the original first, enforces the original court/time/scope and scheduling window, then authorizes at most one linked child with a newly selected venue credential. Booking rechecks the linked parent and account claims atomically. Parent and child must be inspected/resolved separately; a pending child does not prove success. See [manual duplicate-risk exception](booking-reliability-runbook.md#manual-duplicate-risk-exception-no-automatic-replacement).

## Running Locally

These are manually authorized operator examples, **not agent verification commands**. They contact real Eversports accounts and the mutations can create or cancel actual bookings. Use local test doubles for development. Booking timestamps must retain the facility's local offset, not `Z`/UTC.

```bash
# Set explicit secrets/scope and a disposable PostgreSQL URL. Do not point
# development runs at production data.
export INTERNAL_API_SECRET="$(openssl rand -hex 32)"
export BOOKING_MONITORING_SECRET="$(openssl rand -hex 32)"
export BOOKING_IDENTITY_KEY="$(openssl rand -hex 32)"

INTERNAL_API_SECRET="$INTERNAL_API_SECRET" \
  BOOKING_MONITORING_SECRET="$BOOKING_MONITORING_SECRET" \
  BOOKING_IDENTITY_KEY="$BOOKING_IDENTITY_KEY" \
  BOOKING_RELIABLE_CUTOVER_ACK=keyed-v2-only \
  BOOKING_DATABASE_URL="postgres://squash_bot:squash_bot@localhost:7432/squash_bot?sslmode=disable" \
  BOOKING_SERVICE_KEY=eversports.primary.squash \
  EVERSPORTS_FACILITY_ID=76443 \
  EVERSPORTS_FACILITY_SLUG=squash-house-berlin-03 \
  EVERSPORTS_FACILITY_UUID=6266968c-b0fd-4115-ad3b-ae225cc880f1 \
  EVERSPORTS_SQUASH_SPORT_UUID=b388b6e6-69de-11e8-bdc6-02bd505aa7b2 \
  go run ./cmd/booking

# In the caller shell, with the same INTERNAL_API_SECRET:
# List court slots for a date (credentials passed per-request via headers)
curl -H "Authorization: Bearer $INTERNAL_API_SECRET" \
  -H "X-Eversports-Email: you@example.com" \
  -H "X-Eversports-Password: secret" \
  "http://localhost:8081/api/v1/eversports/matches?date=2026-04-12"

# Fetch single booking detail by match UUID
curl -H "Authorization: Bearer $INTERNAL_API_SECRET" \
  -H "X-Eversports-Email: you@example.com" \
  -H "X-Eversports-Password: secret" \
  "http://localhost:8081/api/v1/eversports/matches/<uuid>"

# Create a booking with a fresh UUIDv7 operation key (credentials in JSON body).
curl -X POST -H "Authorization: Bearer $INTERNAL_API_SECRET" \
  -H "Idempotency-Key: <uuid-v7>" -H "Content-Type: application/json" \
  -d '{"courtUuid":"<court-uuid>","start":"2026-04-12T08:45:00+02:00","end":"2026-04-12T09:30:00+02:00","email":"you@example.com","password":"secret"}' \
  http://localhost:8081/api/v2/eversports/matches

# Cancel an exact known/imported match with a different fresh UUIDv7 key.
curl -X DELETE -H "Authorization: Bearer $INTERNAL_API_SECRET" \
  -H "Idempotency-Key: <different-uuid-v7>" -H "Content-Type: application/json" \
  -d '{"email":"you@example.com","password":"secret"}' \
  "http://localhost:8081/api/v2/eversports/matches/<match-uuid>"
```

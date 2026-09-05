# Sports Booking Service

**booking** is a lightweight HTTP service (port 8081) that connects to [Eversports](https://www.eversports.de/) using credentials supplied per request. It reverse-engineers the Eversports internal API to support listing, creating, and cancelling court bookings. There is no service-level default account. For session/checkout development constraints, see [Booking internals](services/booking.md).

## Environment Variables

| Variable                    | Required | Default           | Description                                                    |
|-----------------------------|----------|-------------------|----------------------------------------------------------------|
| `EVERSPORTS_FACILITY_ID`    | No       | _(empty)_         | Numeric facility ID required for `GET /api/v1/eversports/matches` and `GET /api/v1/eversports/courts`. Find it in the venue page URL (e.g. `eversports.de/s/venue-name-76443`). |
| `EVERSPORTS_FACILITY_UUID`  | No       | `6266968c-…`      | UUID of the facility used when creating bookings via `POST /api/v1/eversports/matches`. Find it in the `facilityUuid` field of the `/checkout/api/payableitem/courtbooking` request body in browser DevTools. |
| `EVERSPORTS_FACILITY_SLUG`  | No       | _(empty)_         | Facility slug from the venue URL on eversports.de (e.g. `squash-house-berlin-03`). Required for `GET /api/v1/eversports/matches` and `GET /api/v1/eversports/courts`. |
| `INTERNAL_API_SECRET`       | Yes      | —                 | Shared secret for authenticating calls to this service         |
| `SERVER_PORT`               | No       | `8081`            | HTTP API listen port                                           |
| `LOG_LEVEL`                 | No       | `INFO`            | `INFO` or `DEBUG`                                              |
| `LOG_DIR`                   | No       | _(empty)_         | If set, writes log files to `$LOG_DIR/app.log` with rotation (10 MB / 5 backups, gzip). Stdout logging is always preserved. |
| `TIMEZONE`                  | No       | `UTC`             | Timezone for log timestamps                                    |

## API Endpoints

All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET`  | `/health` | No | Liveness probe |
| `GET`  | `/version` | No | Service version |
| `GET`  | `/api/v1/eversports/matches?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Yes | Court bookings for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means the slot is already reserved. Optionally filter by time window (inclusive) and/or by whether the authenticated user owns the reservation (`my=true\|false`). Court IDs are resolved automatically via `/courts`. Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `POST` | `/api/v1/eversports/matches` | Yes | Create a court booking. Body: `{"courtUuid":"…","start":"…","end":"…","email":"…","password":"…"}` (RFC 3339). Returns `{"bookingUuid":"…","bookingId":…,"matchId":"…"}` (`matchId` omitted if match creation failed). Requires `EVERSPORTS_FACILITY_UUID`. |
| `GET`  | `/api/v1/eversports/matches/{id}` | Yes | Fetch a single booking by its **match UUID** (the `matchId` returned by `POST /matches`) |
| `DELETE` | `/api/v1/eversports/matches/{id}` | Yes | Cancel a booking by its **match UUID** (the `matchId` returned by `POST /matches`, **not** `bookingUuid`). Body: `{"email":"…","password":"…"}`. Returns `{"id":"…","state":"CANCELLED","relativeLink":"…"}`. |
| `GET`  | `/api/v1/eversports/courts[?date=YYYY-MM-DD]` | Yes | List courts at the facility; returns `[{"id":"…","uuid":"…","name":"…"}]`. Parses the Eversports booking calendar HTML. Optional `date` parameter (default: today). Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `GET`  | `/api/v1/eversports/facility?slug=<slug>` | Yes | Venue profile for a facility slug; returns `{"id","slug","name","rating","reviewCount","address","hideAddress","tags","contact","sports","city","company"}`. `slug` query parameter is mandatory (400 if missing). Returns 404 if the slug is not found on Eversports. |

## Authentication

Credentials are supplied **per-request** by the management service — either via `X-Eversports-Email`/`X-Eversports-Password` headers (GET endpoints) or as `email`/`password` fields in the JSON body (POST/DELETE endpoints). All six Eversports endpoints return HTTP 400 if credentials are absent.

The service caches one `*eversports.Client` per unique `email:password` pair in memory (`credClients sync.Map`). Each client manages its own in-memory cookie jar and re-authenticates automatically when the `et` session cookie expires.

## Running Locally

These are manually authorized operator examples, **not agent verification commands**. They contact real Eversports accounts and the mutations can create or cancel actual bookings. Use local test doubles for development. Booking timestamps must retain the facility's local offset, not `Z`/UTC.

```bash
# Set the same generated secret in the service and caller shells (minimum 32 characters).
export INTERNAL_API_SECRET="$(openssl rand -hex 32)"

INTERNAL_API_SECRET="$INTERNAL_API_SECRET" \
  EVERSPORTS_FACILITY_ID=76443 \
  EVERSPORTS_FACILITY_SLUG=squash-house-berlin-03 \
  EVERSPORTS_FACILITY_UUID=6266968c-b0fd-4115-ad3b-ae225cc880f1 \
  go run cmd/booking/main.go

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

# Create a booking (credentials in JSON body)
curl -X POST -H "Authorization: Bearer $INTERNAL_API_SECRET" -H "Content-Type: application/json" \
  -d '{"courtUuid":"<court-uuid>","start":"2026-04-12T08:45:00+02:00","end":"2026-04-12T09:30:00+02:00","email":"you@example.com","password":"secret"}' \
  http://localhost:8081/api/v1/eversports/matches

# Cancel a booking (credentials in JSON body)
curl -X DELETE -H "Authorization: Bearer $INTERNAL_API_SECRET" -H "Content-Type: application/json" \
  -d '{"email":"you@example.com","password":"secret"}' \
  "http://localhost:8081/api/v1/eversports/matches/<uuid>"
```

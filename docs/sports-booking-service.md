# Sports Booking Service

**booking** is a lightweight HTTP service (port 8081) that connects to [Eversports](https://www.eversports.de/) on behalf of a configured user account. It reverse-engineers the Eversports internal API to support listing, creating, and cancelling court bookings.

## Environment Variables

| Variable                    | Required | Default           | Description                                                    |
|-----------------------------|----------|-------------------|----------------------------------------------------------------|
| `EVERSPORTS_EMAIL`          | Yes      | —                 | Eversports account email                                       |
| `EVERSPORTS_PASSWORD`       | Yes      | —                 | Eversports account password                                    |
| `EVERSPORTS_FACILITY_ID`    | No       | _(empty)_         | Numeric facility ID required for `GET /api/v1/eversports/matches` and `GET /api/v1/eversports/courts`. Find it in the venue page URL (e.g. `eversports.de/s/venue-name-76443`). |
| `EVERSPORTS_FACILITY_UUID`  | No       | `6266968c-…`      | UUID of the facility used when creating bookings via `POST /api/v1/eversports/matches`. Find it in the `facilityUuid` field of the `/checkout/api/payableitem/courtbooking` request body in browser DevTools. |
| `EVERSPORTS_FACILITY_SLUG`  | No       | _(empty)_         | Facility slug from the venue URL on eversports.de (e.g. `squash-house-berlin-03`). Required for `GET /api/v1/eversports/matches` and `GET /api/v1/eversports/courts`. |
| `INTERNAL_API_SECRET`       | Yes      | —                 | Shared secret for authenticating calls to this service         |
| `SERVER_PORT`               | No       | `8081`            | HTTP API listen port                                           |
| `LOG_LEVEL`                 | No       | `INFO`            | `INFO` or `DEBUG`                                              |
| `TIMEZONE`                  | No       | `UTC`             | Timezone for log timestamps                                    |

## API Endpoints

All endpoints except `/health` and `/version` require `Authorization: Bearer <INTERNAL_API_SECRET>`.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET`  | `/health` | No | Liveness probe |
| `GET`  | `/version` | No | Service version |
| `GET`  | `/api/v1/eversports/matches?date=YYYY-MM-DD[&startTime=HHMM][&endTime=HHMM][&my=true\|false]` | Yes | Court bookings for a date from the Eversports `/api/slot` endpoint. Each item is a time slot on a specific court; `booking != null` means the slot is already reserved. Optionally filter by time window (inclusive) and/or by whether the authenticated user owns the reservation (`my=true\|false`). Court IDs are resolved automatically via `/courts`. Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `POST` | `/api/v1/eversports/matches` | Yes | Create a court booking. Body: `{"courtUuid":"…","start":"…","end":"…"}` (RFC 3339). Returns `{"bookingUuid":"…","bookingId":…,"matchId":"…"}` (`matchId` omitted if match creation failed). Requires `EVERSPORTS_FACILITY_UUID`. |
| `GET`  | `/api/v1/eversports/matches/{id}` | Yes | Fetch a single booking by its **match UUID** (the `matchId` returned by `POST /matches`) |
| `DELETE` | `/api/v1/eversports/matches/{id}` | Yes | Cancel a booking by its **match UUID** (the `matchId` returned by `POST /matches`, **not** `bookingUuid`). Returns `{"id":"…","state":"CANCELLED","relativeLink":"…"}`. |
| `GET`  | `/api/v1/eversports/courts[?date=YYYY-MM-DD]` | Yes | List courts at the facility; returns `[{"id":"…","uuid":"…","name":"…"}]`. Parses the Eversports booking calendar HTML. Optional `date` parameter (default: today). Requires `EVERSPORTS_FACILITY_ID` and `EVERSPORTS_FACILITY_SLUG`. |
| `GET`  | `/api/v1/eversports/facility?slug=<slug>` | Yes | Venue profile for a facility slug; returns `{"id","slug","name","rating","reviewCount","address","hideAddress","tags","contact","sports","city","company"}`. `slug` query parameter is mandatory (400 if missing). Returns 404 if the slug is not found on Eversports. |

## Authentication

Authentication with Eversports is handled automatically: the service logs in on the first request and re-authenticates if the session expires. Login POSTs the `LoginCredentialLogin` GraphQL mutation to `https://www.eversports.de/api/checkout` and stores the resulting `et` session cookie in an in-memory jar.

## Running Locally

```bash
EVERSPORTS_EMAIL=you@example.com \
  EVERSPORTS_PASSWORD=secret \
  INTERNAL_API_SECRET=test \
  EVERSPORTS_FACILITY_ID=76443 \
  EVERSPORTS_FACILITY_SLUG=squash-house-berlin-03 \
  EVERSPORTS_FACILITY_UUID=6266968c-b0fd-4115-ad3b-ae225cc880f1 \
  go run cmd/booking/main.go

# List court slots for a date (service logs in automatically on first request)
curl -H "Authorization: Bearer test" "http://localhost:8081/api/v1/eversports/matches?date=2026-04-12"

# Fetch single booking detail by match UUID
curl -H "Authorization: Bearer test" http://localhost:8081/api/v1/eversports/matches/<uuid>

# Create a booking
curl -X POST -H "Authorization: Bearer test" -H "Content-Type: application/json" \
  -d '{"courtUuid":"<court-uuid>","start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}' \
  http://localhost:8081/api/v1/eversports/matches

# Cancel a booking
curl -X DELETE -H "Authorization: Bearer test" http://localhost:8081/api/v1/eversports/matches/<uuid>
```

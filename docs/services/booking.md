# Booking service internals

The service wraps the reverse-engineered Eversports API. It has no database; sessions are in-memory and scoped to per-request credentials. [Operator API/configuration](../sports-booking-service.md) is separate from this development reference. Manual API examples may cause real bookings; do not execute them as tests.

## Sources and seams

| Concern | Source |
|---|---|
| Routes, per-credential dispatch, request validation | [booking/handler.go](../../cmd/booking/booking/handler.go) |
| Bearer middleware and HTTP server | [booking/server.go](../../cmd/booking/booking/server.go) |
| Authentication/session/error helpers | [eversports/client.go](../../cmd/booking/eversports/client.go) |
| Checkout and mutation retry boundaries | [eversports/checkout.go](../../cmd/booking/eversports/checkout.go) |
| Match lookup/cancellation | [eversports/matches.go](../../cmd/booking/eversports/matches.go) |
| Courts/slots and calendar HTML parsing | [eversports/slots.go](../../cmd/booking/eversports/slots.go) |
| Facility profile | [eversports/facility.go](../../cmd/booking/eversports/facility.go) |
| Public and shared wire types | [eversports/models.go](../../cmd/booking/eversports/models.go) |

The handler-local `eversportsClient` interface enables fakes. Same-package upstream-client tests override `Client.baseURL` with `httptest.Server`; methods must use that field, not the production URL constant directly. Public types belong in models; operation-private wire types stay near their operation.

## Per-request credentials

GET operations use `X-Eversports-Email` and `X-Eversports-Password`; POST/DELETE use JSON credential fields. All six Eversports endpoints reject missing credentials. `EVERSPORTS_EMAIL`/`EVERSPORTS_PASSWORD` are **not** service configuration and there is no default account fallback.

The handler caches dedicated clients by the email/password pair; password rotation selects a new client. Do not expose cache keys, cookies, or request passwords in logs/responses. Facility configuration is service-wide, so separate instances for different facilities are not interchangeable replicas.

## Session expiry is not just HTTP 401

Eversports may return HTTP 200 with a GraphQL auth error or an HTML login/challenge page.

- A usable session requires the `loggedIn` flag **and** the `et` cookie. The cookie jar can expire the cookie independently.
- Invalidating the session clears the flag and removes the cookie; otherwise a stale cookie can falsely satisfy login checks.
- Route auth-shaped top-level GraphQL errors through `gqlTopLevelError`. Business-rule errors from `ExpectedErrors` must not trigger authentication retries.
- Before decoding a JSON response, use `htmlAuthError` where retry is safe. Court discovery legitimately parses HTML and is an exception.
- Use `bodySnippet` for any response body included in errors; it bounds and normalizes output. It is not a general secret-redaction mechanism.

`withAuth` ensures login, performs the operation, and retries once after a recognized unauthorized response. Match/slot/court/facility methods use it. **CreateBooking does not**: a generic retry around checkout can duplicate a reservation.

## Checkout retry safety

`CreateBooking` holds the client-local `bookingMu` across the flow:

1. Reserve a court through `payableitem/courtbooking`.
2. Settle the payment with `pay-offline`.
3. Attach the match record (best-effort).
4. Report marketplace fee (best-effort).
5. Track checkout completion (best-effort).

The implemented retry is limited to recognized unauthorized/HTML responses at step 1. Do not generalize it to arbitrary timeouts or other ambiguous failures. **Step 2 must not restart checkout**: a reservation may already exist. Failures in steps 3–5 are logged but do not discard the booking result; `matchId` can be absent.

The lock prevents same-client checkout interleaving caused by implicit server state. It is not distributed/account-wide coordination and does not make multiple replicas safe. Management's own credential-rotation/error policy has separate ambiguity risks; see [scheduling](management-scheduling.md#booking-and-publication-lifecycle).

Booking timestamps must retain the facility/group local UTC offset. Do not convert them to `Z`/UTC before sending to Eversports. The integration currently uses squash constants; it is not a provider-neutral multi-sport booking API.

## Verification

[client_test.go](../../cmd/booking/eversports/client_test.go) covers cookie loss, auth-shaped errors, the pay-offline no-retry boundary, an ambiguous step-1 gateway timeout, and post-payment partial success; [bookings_test.go](../../cmd/booking/eversports/bookings_test.go) covers calendar parsing; [handler_test.go](../../cmd/booking/booking/handler_test.go) covers handler behavior with fakes. Run these locally, not against real accounts.

When modifying an operation, test its real response shapes: normal JSON, top-level errors, business errors, HTML, and relevant HTTP failures. Existing mocked retry tests do not establish end-to-end exactly-once booking or compatibility with an upstream change that has not been captured in fixtures.

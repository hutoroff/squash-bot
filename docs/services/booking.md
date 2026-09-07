# Booking service internals

The service wraps the reverse-engineered Eversports API. Its running binary still has no database dependency; sessions are in-memory and scoped to per-request credentials. The shared [booking API v2 contract foundation](../../internal/bookingapi), typed provider mutation failures, booking-owned [durable storage package](../../cmd/booking/storage), and [execution coordinator/session manager](../../cmd/booking/service) now exist, but startup/handlers do not use them yet. Handlers still expose the legacy non-idempotent API: no status routes, durable admission, or v2 mutation dispatch is wired. The staged target is in [booking reliability](../booking-reliability-design.md). [Operator API/configuration](../sports-booking-service.md) is separate from this development reference. Manual API examples may cause real bookings; do not execute them as tests.

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
| Booking-owned schema, admission, evidence and singleton ownership | [storage](../../cmd/booking/storage) |
| Tracked execution and bounded account/session coordination | [service](../../cmd/booking/service) |
| Public and shared wire types | [eversports/models.go](../../cmd/booking/eversports/models.go) |

The handler-local `eversportsClient` interface enables fakes. Same-package upstream-client tests override `Client.baseURL` with `httptest.Server`; methods must use that field, not the production URL constant directly. Public types belong in models; operation-private wire types stay near their operation.

## Per-request credentials

GET operations use `X-Eversports-Email` and `X-Eversports-Password`; POST/DELETE use JSON credential fields. All six Eversports endpoints reject missing credentials. `EVERSPORTS_EMAIL`/`EVERSPORTS_PASSWORD` are **not** service configuration and there is no default account fallback.

The handler caches dedicated clients by the email/password pair; password rotation selects a new client. Do not expose cache keys, cookies, or request passwords in logs/responses. Facility configuration is service-wide, so separate instances for different facilities are not interchangeable replicas.

## Durable execution coordination (not wired yet)

The coordinator admits at most one external mutation flow, serializes same-key submissions locally before durable admission, and rechecks the ledger after an uncertain admission acknowledgment. Matching replays—including password rotation and a fresh process cache—return the stored operation without constructing a session or invoking the provider runner. Accepted execution uses a service-owned deadline rather than the caller context; draining blocks new submissions, while singleton-connection loss permanently closes admission and cancels tracked executors for conservative finalization/recovery.

The session manager keys account locks by the durable HMAC account identity, not password or management credential entity. Its process-local credential-version digest selects disposable sessions under the same lock domain. Construction is atomic, leases pin active sessions, obsolete rotated sessions are closed, and idle LRU storage is bounded to 32 entries with a 15-minute default. A dedicated PostgreSQL connection holds the single-process advisory guard and never reacquires it after a health-check failure.

The local P4 `ProviderRunner` uses phase-aware Eversports methods that each send at most one reserve, payment, attachment, or cancellation request. It persists an executor-fenced intent before each send and sanitized evidence afterward using a bounded caller-independent persistence context, then persists reservation/payment/match identifiers before advancing. Only a durable no-effect authentication result can authorize the existing single retry. Payment or cancellation ambiguity stops provider work; optional attachment failure retains confirmed booking evidence with a warning. Provider-user IDs are converted to keyed aliases before persistence, suspect sessions are discarded, unsafe redirects are not followed, and booking idempotency keys are never forwarded to Eversports. Coordinator, runner, storage, and guard still remain runtime seams until P5 recovery/API controls and P6 startup/shutdown configuration are complete.

## Session expiry is not just HTTP 401

Eversports may return HTTP 200 with a GraphQL auth error or an HTML login/challenge page.

- A usable session requires the `loggedIn` flag **and** the `et` cookie. The cookie jar can expire the cookie independently.
- Invalidating the session clears the flag and removes the cookie; otherwise a stale cookie can falsely satisfy login checks.
- Route auth-shaped top-level GraphQL errors through `gqlTopLevelError`. Business-rule errors from `ExpectedErrors` must not trigger authentication retries.
- Before decoding a JSON response, use `htmlAuthError` where retry is safe. Court discovery legitimately parses HTML and is an exception.
- Use `bodySnippet` for any response body included in errors; it bounds and normalizes output. It is not a general secret-redaction mechanism.

`withAuth` ensures login, performs the operation, and retries once after a recognized unauthorized response. Match/slot/court/facility methods use it. **CreateBooking does not**: a generic retry around checkout can duplicate a reservation.

## Checkout retry safety

Legacy `CreateBooking` holds the client-local `bookingMu` across the flow and delegates to the same one-send phase primitives used by the durable runner. Create/cancel failures are exposed as typed `MutationError` values carrying provider phase, effect certainty, code, and narrowly scoped retry safety. `AuthenticationError` supports a positively identified credential-rejection marker, but current reverse-engineered login `ExpectedErrors` have no captured stable ID: they conservatively remain `authentication_failed` and must not trigger credential cooldown from message text. Existing HTTP handlers have not yet mapped these to the planned v2 operation envelope.

The checkout flow is:

1. Reserve a court through `payableitem/courtbooking`.
2. Settle the payment with `pay-offline`.
3. Attach the match record (best-effort).
4. Report marketplace fee (best-effort).
5. Track checkout completion (best-effort).

The implemented retry is limited to a typed, recognized no-effect unauthorized/HTML response at step 1. Do not generalize it to arbitrary timeouts or other ambiguous failures. Reserve transport/HTTP/malformed/rejection-shaped failures are typed unknown unless an existing narrow no-effect rule applies; required reservation/payment identifiers are validated. **Step 2 must not restart checkout**: a reservation may already exist. Its body must be a JSON object without a negative success/errors indication, but this acknowledgment is still not a documented provider settlement guarantee. Failures in steps 3–5 are logged but do not discard the booking result; `matchId` can be absent.

Cancellation validates that the response is a `BallsportMatch` for the requested ID in `CANCELLED` state. Expected business errors are typed no-effect; malformed, mismatched, non-confirmed and transport/HTTP results are unknown and not retry-safe. `CancelMatchOnce` never retries inside the adapter; the durable runner can issue a second attempt only after the first attempt's no-effect authentication evidence and retry login are journaled. These classifications and journal boundaries do not prove provider-side exactly-once behavior.

The lock prevents same-client checkout interleaving caused by implicit server state. It is not distributed/account-wide coordination and does not make multiple replicas safe. Management's own credential-rotation/error policy has separate ambiguity risks; see [scheduling](management-scheduling.md#booking-and-publication-lifecycle).

Booking timestamps must retain the facility/group local UTC offset. Do not convert them to `Z`/UTC before sending to Eversports. The integration currently uses squash constants; it is not a provider-neutral multi-sport booking API.

## Verification

[client_test.go](../../cmd/booking/eversports/client_test.go) covers cookie loss, auth-shaped errors, the pay-offline no-retry boundary, an ambiguous step-1 gateway timeout, unsafe redirect suppression, one-send cancellation, and post-payment partial success; [bookings_test.go](../../cmd/booking/eversports/bookings_test.go) covers calendar parsing; [handler_test.go](../../cmd/booking/booking/handler_test.go) covers handler behavior with fakes. Run these locally, not against real accounts.

When modifying an operation, test its real response shapes: normal JSON, top-level errors, business errors, HTML, and relevant HTTP failures. `internal/bookingapi/validation_test.go` pins UUID, scope, local-offset/millisecond, HMAC account-identity, password-independent fingerprint and duplicate-risk request rules; `mutation_error_test.go` pins representative phase/effect classifications. Integration-tagged storage tests use disposable PostgreSQL to pin independent migration order, booking-only initialization, concurrent operation admission, account/resource conflicts, explicit replacement limits, immutable evidence, singleton exclusion and guard-connection loss. Coordinator/session tests pin concurrent replay, lost admission acknowledgments, caller-independent execution, fail-closed authority loss, restart/cache replacement, suspect-session discard and account-scoped cache serialization. Provider-runner tests pin intent/evidence ordering, local-offset preservation, safe authentication retry, persistence/authority-loss boundaries, conservative ambiguity and paid-booking attachment warnings; integration-tagged PostgreSQL coverage verifies that journaled runner evidence satisfies durable finalization. Since these layers are not wired to handlers, the checks do not establish end-to-end exactly-once booking or compatibility with an upstream change not captured in fixtures.

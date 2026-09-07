# Booking service internals

The service wraps the reverse-engineered Eversports API and now starts in reliable v2 mode. It runs booking-owned migrations, verifies the durable identity-key marker, acquires PostgreSQL singleton authority, performs evidence-only recovery, and then attaches the coordinator, status, reconciliation, import and retention services. Provider sessions and credentials remain bounded memory-only state; authoritative operation, ownership and conflict state is in PostgreSQL. The remaining staged work is management's mandatory v2 integration and later discovery attachment; see [booking reliability](../booking-reliability-design.md). [Operator API/configuration](../sports-booking-service.md) is separate from this development reference. Manual API examples may cause real bookings; do not execute them as tests.

## Sources and seams

| Concern | Source |
|---|---|
| Routes, per-credential dispatch, request validation | [booking/handler.go](../../cmd/booking/booking/handler.go) |
| Business/admin HTTP servers, readiness and metrics | [booking/server.go](../../cmd/booking/booking/server.go), [booking/admin.go](../../cmd/booking/booking/admin.go) |
| Startup, recovery, retention and shutdown wiring | [main.go](../../cmd/booking/main.go), [runtime.go](../../cmd/booking/runtime.go) |
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

## Durable execution coordination

The coordinator admits at most one external mutation flow, serializes same-key submissions locally before durable admission, and rechecks the ledger after an uncertain admission acknowledgment. Matching replays—including password rotation and a fresh process cache—return the stored operation without constructing a session or invoking the provider runner. Accepted execution uses a service-owned deadline rather than the caller context; draining blocks new submissions, while singleton-connection loss permanently closes admission and cancels tracked executors for conservative finalization/recovery.

The session manager keys account locks by the durable HMAC account identity, not password or management credential entity. Its process-local credential-version digest selects disposable sessions under the same lock domain. Construction is atomic, leases pin active sessions, obsolete rotated sessions are closed, and idle LRU storage is bounded to 32 entries with a 10-minute runtime default. A dedicated PostgreSQL connection holds the single-process advisory guard and never reacquires it after a health-check failure.

The `ProviderRunner` uses phase-aware Eversports methods that each send at most one reserve, payment, attachment, or cancellation request. It persists an executor-fenced intent before each send and sanitized evidence afterward using a bounded caller-independent persistence context, then persists reservation/payment/match identifiers before advancing. Only a durable no-effect authentication result can authorize the existing single retry. Payment or cancellation ambiguity stops provider work; optional attachment failure retains confirmed booking evidence with a warning. Provider-user IDs are converted to keyed aliases before persistence, suspect sessions are discarded, unsafe redirects are not followed, and booking idempotency keys are never forwarded to Eversports. Startup configures a 20-second provider request timeout inside the 90-second service-owned execution budget.

The configurable v2 handler requires one canonical UUID `Idempotency-Key`, validates strict bounded DTOs against trusted scope, and uses the coordinator without forwarding the key upstream. Mutation responses return the durable operation envelope: accepted/running `202`, completed creation `201`, completed cancellation/no-op `200`, no-effect `422`, and unknown `409`; status reads return ledger state without provider credentials and tombstones return `410`. Once reliable dependencies are attached, v1 POST/DELETE return `booking_contract_upgrade_required` before decoding credentials or contacting Eversports. Cancellation admission fails closed unless the exact match/account/facility is already represented by provider evidence; a confirmed already-cancelled target creates a durable successful no-op. Legacy targets must first pass the credential-verified import route; an empty ledger is never treated as proof that a target is safe.

`POST /api/v2/eversports/operations/{id}/reconcile` uses supplied credentials in a fresh isolated session and performs only the existing match read. It coalesces concurrent local probes and automatically associates only a match ID already tied to an unknown operation. `POST .../{id}/resolution` additionally requires an idempotency key, expected revision, explicit match ID, actor and reason. Both require the credential-derived account identity to match, verify the exact ID plus configured facility/sport and creation times, and refuse missing, mismatched or cancellation-active evidence without changing blockers. A positive match proves creation success under the existing pay-before-attach flow; only `CANCELLED` proves cancellation success. Sanitized evidence, provenance, outcome and claim changes commit atomically after the provider response using a bounded caller-independent context. Same-key resolution replay performs no provider read, while changed reuse conflicts. These controls never reserve, pay, attach or cancel.

`POST /api/v2/eversports/imports` requires an idempotency key and validates an exact match plus configured facility/sport, a unique court UUID/name, and an exact date/start/court slot that the authenticated account owns from the provider calendar. It writes a dedicated `booking_imports` record, provider ownership and occupancy claim rather than inventing a successful historical create operation. Active imports block overlapping bookings and can be cancelled only by the matching account; confirmed cancellation atomically updates the import and releases its claim. Replay with rotated credentials returns the same imported status without provider reads, while changed provenance or a match/claim already owned elsewhere conflicts. Provider-local offset strings are retained.

The retention service defaults to one bounded batch of 100 operations older than 90 days after the later of terminal completion and booking end. Compaction skips active claims, unresolved replacement lineages and active provider bookings. It retains operation keys, fingerprints, terminal summaries, step classifications, provider ownership and idempotency/provenance events, while marking the result expired and physically replacing detailed step payloads with `{}`. Runtime runs one bounded pass at startup and every configured retention interval.

Startup recovery runs after singleton acquisition and before coordinator construction. It finalizes from durable steps/provider IDs only: confirmed payment/cancellation succeeds, all mutation attempts proved no-effect reject, no mutation intent is not-dispatched, and every remaining case is unknown. It never constructs a session or calls Eversports.

## Runtime health and lifecycle

The public business listener keeps only `/health` unauthenticated as a compatibility liveness alias. Its legacy `/version` route and every business route require `INTERNAL_API_SECRET`. A separate, non-published admin listener requires `BOOKING_MONITORING_SECRET` for `/health/live`, `/health/ready`, `/version`, `/descriptor` and `/metrics`; the monitoring token is rejected by the business listener. Readiness requires database access, healthy singleton ownership and an accepting coordinator. Metrics use the official Go/process collectors and bounded route, durable-outcome, provider-flow, running-operation and unresolved-operation labels/gauges; IDs and accounts are never labels.

On signal or singleton loss, the process closes admission first, stops both HTTP listeners, drains tracked execution within the shared shutdown budget, closes in-memory sessions, releases singleton authority and closes the pool. The process never reacquires singleton authority after connection loss. Production Compose gives booking 110 seconds of stop grace for the default 100-second shutdown budget and probes authenticated admin readiness. Backup, replacement, cutover, incident, and keyless-rollback restrictions are in the [reliable booking runbook](../booking-reliability-runbook.md).

## Session expiry is not just HTTP 401

Eversports may return HTTP 200 with a GraphQL auth error or an HTML login/challenge page.

- A usable session requires the `loggedIn` flag **and** the `et` cookie. The cookie jar can expire the cookie independently.
- Invalidating the session clears the flag and removes the cookie; otherwise a stale cookie can falsely satisfy login checks.
- Route auth-shaped top-level GraphQL errors through `gqlTopLevelError`. Business-rule errors from `ExpectedErrors` must not trigger authentication retries.
- Before decoding a JSON response, use `htmlAuthError` where retry is safe. Court discovery legitimately parses HTML and is an exception.
- Use `bodySnippet` for any response body included in errors; it bounds and normalizes output. It is not a general secret-redaction mechanism.

`withAuth` ensures login, performs the operation, and retries once after a recognized unauthorized response. Match/slot/court/facility methods use it. **CreateBooking does not**: a generic retry around checkout can duplicate a reservation.

## Checkout retry safety

Legacy `CreateBooking` holds the client-local `bookingMu` across the flow and delegates to the same one-send phase primitives used by the durable runner. Create/cancel failures are exposed as typed `MutationError` values carrying provider phase, effect certainty, code, and narrowly scoped retry safety. `AuthenticationError` supports a positively identified credential-rejection marker, but current reverse-engineered login `ExpectedErrors` have no captured stable ID: they conservatively remain `authentication_failed` and must not trigger credential cooldown from message text. The reliable handler maps durable terminal classifications to the v2 operation envelope; legacy mutation routes remain blocked rather than exposing older ambiguous semantics.

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

When modifying an operation, test its real response shapes: normal JSON, top-level errors, business errors, HTML, and relevant HTTP failures. `internal/bookingapi/validation_test.go` pins UUID, scope, local-offset/millisecond, HMAC account-identity, password-independent fingerprint and duplicate-risk request rules; `mutation_error_test.go` pins representative phase/effect classifications. Integration-tagged storage tests use disposable PostgreSQL to pin independent migration order, booking-only initialization, concurrent operation admission, account/resource conflicts, explicit replacement limits, immutable evidence, singleton exclusion and guard-connection loss. Coordinator/session tests pin concurrent replay, lost admission acknowledgments, caller-independent execution, fail-closed authority loss, restart/cache replacement, suspect-session discard and account-scoped cache serialization. Provider-runner tests pin intent/evidence ordering, local-offset preservation, safe authentication retry, persistence/authority-loss boundaries, conservative ambiguity and paid-booking attachment warnings. Recovery/status tests pin evidence-only crash classification and sanitized/tombstoned views; reconciliation/import tests pin coalesced exact-match reads, account/scope/time/court checks, insufficient-evidence refusal, idempotent replay and post-response persistence; retention tests pin the bounded 90-day policy; reliable-handler tests pin operation/import keys, strict request bodies, response mappings, control routes, no-credential status and legacy mutation gating. Integration-tagged PostgreSQL coverage verifies journaled runner evidence, paid-booking recovery, known and imported cancellation ownership, already-cancelled no-op behavior, atomic verified resolution, imported occupancy release, migration compatibility and compaction blockers/scrubbing. The integration-tagged runtime test additionally exercises migrations, identity-key verification, singleton exclusion, a direct fake-provider creation flow, orderly stop/restart with same-key replay, injected disposable-database loss, fail-closed database/singleton readiness and admission, and shutdown against disposable PostgreSQL. These checks establish local runtime attachment, not provider-side exactly-once behavior or compatibility with an upstream change not captured in fixtures.

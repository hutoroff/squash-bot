# Booking reliability and discovery preparation

**Status:** Implementation in progress. P1's shared v2 contract/failure foundation and P2's booking-owned persistence are committed. P3's single-flow execution coordinator, account-scoped bounded session manager and fail-closed PostgreSQL singleton guard are implemented and tested locally, but booking startup, handlers and provider mutation phases do not use them yet. P4–P7, management integration and discovery attachment remain proposed and unimplemented. Owner decisions below remain authoritative for those tasks.

**Code baseline reviewed:** `78fcc52`.

**Scope:** Make the Eversports booking process replaceable, add durable API idempotency and business-conflict protection, and prepare its lifecycle for [service discovery](service-discovery-design.md). Keep one active booking instance. Management integration is a separate task, specified in [discovery §10.1](service-discovery-design.md#101-management-integration-with-the-reliable-booking-contract).

## 1. Decision and feasibility

This is achievable with **stateless application instances backed by durable booking-owned tables**. Stateless means that replacing a process loses only disposable caches and in-flight execution, not the authoritative operation history or permission to execute another mutation. It does not mean a database-free system.

The target API guarantee (not implemented yet) is:

> An accepted operation key identifies one immutable request. Repeating it returns its recorded result or current status; it never starts another external execution. A different payload under the same key conflicts. An ambiguous external outcome remains recorded and blocks ordinary conflicting work.

A caller may explicitly authorize a **different operation** after an unknown creation outcome. This intentionally accepts a possible duplicate; it is not a retry of the old operation and does not erase the old uncertainty. The owner prefers this exceptional, caller-controlled risk over missing a booking. Booking itself never invents that authorization.

**Not guaranteed:** exactly-once effects at Eversports, automatic completion after every crash, or complete automatic reconciliation. The implemented reverse-engineered API has no established provider idempotency or transaction protocol covering reserve/payment/match creation. A provider may act before the response is lost. A PostgreSQL transaction, lock, discovery lease, or fresh login cannot close that gap.

### Confirmed choices

- Keep disposable, bounded session caches; correctness must survive their complete loss.
- Booking directly accesses the **same PostgreSQL database and schema as management, using separate booking-owned tables**. Do not introduce a separate database/schema, Redis, or coordinator service.
- Booking owns these tables and migrations. No reads/writes of management business or registry tables, and no management HTTP dependency for operation admission or execution.
- Booking must work with management stopped, given its database, local configuration, credentials supplied by the caller, and reachable Eversports. A shared database outage still affects both services.
- Single-active booking deployment, stop-before-start replacement. Multi-active support is a later gate even if persistence tests exercise concurrent requests/connections.
- Management is currently the only application caller. Its implementation is deferred; production mutation cutover waits for that task.
- Request deduplication and cross-request booking conflicts are both required. Confirmed cancellation permits deliberate rebooking with a new key.
- An accepted execution may continue after client disconnect, under a bounded service-owned deadline. No offline command queue or automatic restart replay.
- No durable storage of provider passwords or session cookies in booking. The caller supplies credentials for fresh execution and provider reconciliation.
- Automatic caller policy may explicitly authorize one exceptional replacement creation. No human confirmation is required by the booking contract, but authorization and a reason are required per operation.
- Never overlap a replacement with a known active local checkout. Uncertain provider processing after a stopped/crashed executor remains part of the explicitly accepted risk.
- Cancellation has no equivalent automatic duplicate-risk override in this first contract.
- Prepare contracts/admin health/metrics/draining and registration seams now; attach real registration/heartbeats only when shared discovery infrastructure exists.

### Non-goals

No management/Telegram/web implementation in the booking work packages; no new provider, multi-facility selection UI, public API exposure, booking database isolation project, generic workflow engine, catch-up scheduling, automatic duplicate cleanup, or claim that independent browser actions are coordinated.

## 2. Current behavior and gaps

Sources, rather than stale comments in these files, are authoritative:

| Source | Observed behavior | Required change |
|---|---|---|
| [Handler](../cmd/booking/booking/handler.go) | Credentials on every provider request; cache keyed by raw email/password; separate cache `Load`/`Store`; no request IDs. | Durable operation admission; injectable cache/session factory; atomic bounded cache construction; no secret-bearing cache identifiers in diagnostics. |
| [Client](../cmd/booking/eversports/client.go) | Cookie jar, login state, login/checkout locks live in a client. Password rotation selects a different client/lock. | Separate account coordination from a credential-version cache; serialize session-affecting work safely. |
| [Checkout](../cmd/booking/eversports/checkout.go) | Reserve → pay → attach match → tracking. Intermediate booking/payment IDs are local variables. HTTP 200/non-HTML payment is treated as success without semantic validation. | Journal mutation boundaries and identifiers; typed outcomes; validate recognized success shapes; preserve partial results. |
| [Cancellation](../cmd/booking/eversports/matches.go) | Mutation with narrow auth retry; returned ID/state are not strictly checked for confirmed cancellation. | Positive cancellation evidence, durable deduplication, unknown-outcome handling. |
| [Server](../cmd/booking/booking/server.go) | Request context drives checkout; 30s HTTP write timeout and 10s shutdown; health is unconditional liveness. | Separate response/execution budgets, durable finalization, draining and readiness. |
| [Management client](../cmd/management/service/booking_client.go) | Static URL; 30s timeout; no operation key/status or typed execution outcome. | Separate management task, before production activation. |
| [Management orchestration](../cmd/management/service/booking_core.go) | Every create error can rotate credentials; successful results without match ID are not saved as court bookings. | Separate management task must retain intent and partial results and prohibit implicit ambiguous reexecution. |
| [Migrations/test helper](../internal/testutil/testdb.go) | Management still owns the root migration stream; the helper now also supports an empty disposable database for independently ordered streams. | Booking's independent stream/history and both migration orders are covered; startup wiring remains. |

Existing [client tests](../cmd/booking/eversports/client_test.go) cover selected auth retries, a gateway timeout, payment HTML, and post-payment optional failures. They do not establish provider idempotency, account-wide isolation, durable deduplication, semantic payment acknowledgment, or crash recovery. No live provider calls were made for this plan.

## 3. Ownership and storage

```text
management ──HTTP v2──▶ booking ──HTTP──▶ Eversports
     │                    │
     SQL                  SQL
     ▼                    ▼
           same PostgreSQL database/schema
     management tables    booking_* tables
```

Management owns business intent, credential entities, game links, user authorization, and caller risk policy. Booking owns admission, deduplication, external-execution evidence, account/court conflict protection, and API results. Neither owns the other's SQL. The shared schema is a deployment choice, not a distributed transaction across the two services or a security-isolation guarantee.

### Booking-owned table responsibilities

P2 implements these tables in [`cmd/booking/storage/migrations`](../cmd/booking/storage/migrations). Typed selection/state columns and constraints hold authoritative workflow state; bounded JSON is limited to sanitized evidence/event extensions, and PostgreSQL stores the operation instants.

| Table | Responsibility / important constraints |
|---|---|
| `booking_operations` | Immutable operation UUID/key and request fingerprint, kind, provider/facility scope, account reference, court/interval or target booking, current outcome/phase, executor incarnation, revision, timestamps/deadline, expiry marker and parent/root operation for risk-authorized creation. Unique operation key in the booking ledger namespace. |
| `booking_operation_steps` | Durable dispatch-intent and sanitized response/evidence journal for reserve, pay, attach and cancel, with unique phase/attempt/status records and executor-conditional writes. No credentials, cookies, or raw provider bodies. |
| `booking_accounts` | Stable account coordination identity, known provider-user alias when available, active operation and separate quarantine association. Password changes and different management credential rows do not create independent account locks. |
| `booking_resource_claims` | Active/uncertain court interval and cancellation-target claims; operation lineage links. Account/court admission and claim changes are atomic with operation acceptance. A risk replacement adds a linked claim; it does not delete its ancestor's claim. |
| `booking_provider_bookings` | Known external reservations, payment evidence, optional match ID, cancellation state and source operations. Persist even without a match ID. This is provider evidence, not a copy of management's games/court labels. |
| `booking_operation_events` | Durable idempotent event records with bounded reason/evidence and expected/resulting revisions. P2 supplies the repository; later reconciliation/resolution flows must write their provenance transactionally rather than as best-effort logs. |
| `booking_metadata` | Immutable ledger metadata and a non-secret key-verification marker so a changed identity key cannot silently create a parallel account namespace. |
| `booking_schema_migrations` | Booking's own migration history. Never reuse management's `schema_migrations`. |

Compact expired operations in place or in an equivalently authoritative tombstone store; preserve key uniqueness, request fingerprint, terminal summary, provider references and replacement lineage. Never let cleanup turn an old key into a new executable request.

### Database and migration implementation

- P2 adds a booking-local embedded migration stream under `cmd/booking/storage/migrations/`, using existing pgx/golang-migrate dependencies. `booking_schema_migrations` gives it both dedicated history and, through the PostgreSQL driver's lock derivation, a separate migration-lock namespace. Disposable-database tests cover both stream orders and booking-only operation admission.
- Booking startup does not invoke this stream yet; that wiring belongs to P6. It must run only booking migrations. Management continues to run its root `migrations/` stream. Do not alter already-applied management migrations or run all management migrations merely to start booking.
- Keep new names prefixed `booking_`; do not add cross-service foreign keys, cascades, triggers, or shared schema-version ownership.
- Use `BOOKING_DATABASE_URL` as explicit booking configuration, initially pointing to the existing database/schema. Do not silently read management configuration or log the DSN. Sharing the existing role may be retained initially; it is coarse trust, not enforced table isolation. Table-scoped roles can be added separately.
- Use a small bounded pool; budget the additional connections together with management and the dedicated singleton-guard connection. Start with five ordinary connections plus one guard connection and validate under load.
- Start admin health before bounded dependency initialization. Database unavailability keeps readiness false and prevents mutations; schema incompatibility/dirty migration is a visible operator error, not permission to ignore migrations.
- Retain tables on rollback. Backups must include the booking tables and migration history. Restoring an older backup can lose evidence of already-performed external actions: do not resume mutations after restore until reconciliation/operator review establishes a safe boundary.

## 4. Identity and admission

### Separate identities

1. **Logical pool/facility scope:** stable service key plus immutable provider/facility/sport configuration. Never an instance address.
2. **Caller business intent:** persisted by management before choosing an execution; opaque correlation only to booking, not a substitute for admission checks.
3. **Operation key:** caller-generated UUID, durable before dispatch and unchanged on retries. Use `Idempotency-Key`; the same UUID is the lookup ID. Keys are unique across kinds and facility scopes within this booking ledger, so accidental reuse with another scope conflicts rather than dispatching elsewhere.
4. **Provider account:** not a management credential ID, password, group, venue row, or instance. Conservatively normalize login whitespace/case for coordination, without stripping dots or plus suffixes. Keep the actual submitted login for authentication in request memory. A stable keyed fingerprint avoids storing plaintext login as a coordination identifier.
5. **Provider booking identity:** provider + facility + booking identifiers, with optional match ID. UUIDs with different meanings must not be substituted for one another.

Use a dedicated durable `BOOKING_IDENTITY_KEY` for HMAC account fingerprints; do not derive these from passwords or a rotating bearer token. Losing/changing it must not silently split existing account identities: require a controlled identity migration or fail startup on key-version mismatch. Cache credential-version fingerprints may use a separate process-local secret. If login supplies a stable provider user ID, bind its fingerprint as an account alias before mutation and honor existing conflicts. Alias discovery changes coordination lookup, not the already stored request fingerprint; replay identity must remain stable without another provider login. Alternate logins not demonstrably mapped to one provider account remain an explicit limitation; the confirmed same-login/multiple-entity case is covered without upstream research.

### Request fingerprint

Canonicalize and hash only validated business fields: operation kind, contract version, logical scope, account identity, court and interval or cancellation target, and risk-authorization parent/root/acknowledgment/reason. Include fields affecting execution; keep trace metadata separate. Exclude passwords, bearer tokens, cookies, instance identity, and incidental JSON ordering. Password rotation can therefore replay an operation's recorded result without executing it again; changing accounts under the same key conflicts.

Normalize timestamps to the actual supported provider precision before fingerprinting; reject unsupported precision rather than silently alter a booking. Use UTC instants for half-open interval conflicts, while storing and sending the validated local-offset representation to Eversports. Do not serialize provider timestamps as UTC merely for hash convenience. Equivalent offset representations of the same instants must not create two resource identities. Existing group-local/DST behavior stays intact.

### Atomic admission

1. Authenticate the internal caller; validate bounded JSON, required key, explicit facility configuration, timestamps/target, and risk fields. Reject malformed/unsupported requests before external work.
2. Check existing key first: matching fingerprint returns status/result; mismatched fingerprint returns `409 idempotency_key_conflict`. Replays do not log in to Eversports.
3. Obtain bounded execution capacity, then atomically insert the operation and acquire its account/resource claims under a documented common lock order. Use unique constraints and locked resource rows plus overlap queries for interval conflicts; do not rely on an unlocked check-then-insert. Handle races/deadlocks with bounded database-only retries before dispatch.
4. Persist acceptance before beginning provider mutation. If commit acknowledgment is lost, query by the same key to determine acceptance; never treat an uncertain commit as permission for another execution.
5. Start exactly one local executor for the accepted operation. If it cannot start, finalize as `not_dispatched`; do not enqueue indefinitely or execute it on replay.

Court claims are provider/facility/court/time-overlap scoped **across accounts, groups and credential entities**. Cancellation claims are scoped to the provider booking/match identity, not to the request key. A different key cannot bypass either check. Different time intervals on the same court are allowed when they do not overlap; duplicate-risk authorization is the sole exceptional create-conflict path.

Terminal no-effect outcomes release their resource/account claims transactionally. Successful checkout releases executor ownership, but retains the reservation's occupancy claim; retain a separate account quarantine if session/account state is still uncertain. Known confirmed or uncertain reservations remain blockers for overlapping intervals. Confirmed cancellation releases only that reservation's occupancy claim; it cannot clear other unresolved claims for the same slot. Replaying the original create after cancellation still returns its original creation outcome, never rebooks the court. A deliberate rebooking uses a new operation.

## 5. HTTP contract

Declare **`eversports-booking` major 2**. Requiring keys, returning operation status and changing failure meanings are not backward-compatible minor additions. Keep release version separate; never edit `cmd/*/VERSION` as part of implementation.

Proposed v2 routes are a contract sketch to pin with tests in package P1:

| Operation | Proposed route | Behavior |
|---|---|---|
| Create | `POST /api/v2/eversports/matches` | Required `Idempotency-Key`, credentials, court/time; optional explicit risk-authorized replacement fields. |
| Cancel | `DELETE /api/v2/eversports/matches/{match_id}` | Required key and credentials; validated target scope. Persist operation even when confirmed target state makes mutation unnecessary. |
| Status | `GET /api/v2/eversports/operations/{operation_id}` | Stored state/result only, no Eversports call or provider credentials required. Missing record is not evidence of provider failure. |
| Reconcile | `POST /api/v2/eversports/operations/{operation_id}/reconcile` | Supplied credentials, bounded provider **reads** plus evidence persistence; never resume checkout/cancellation. Coalesce concurrent probes and idempotently apply the same evidence/revision, without duplicate transitions/events. |
| Evidence resolution | `POST /api/v2/eversports/operations/{operation_id}/resolution` | Authenticated, idempotent resolution action with key and expected revision; validated provider references/evidence and recorded provenance. No reset-to-pending, evidence-free “mark failed,” or execution side effect. |
| Legacy evidence import | `POST /api/v2/eversports/imports` | Required key; validate supplied provider references/scope with credentialed reads and record evidence/claims without any booking mutation. Mark provenance as imported, not as proof an old application request was idempotent. |

Provide the existing read functionality under the v2 contract as well; keep v1 GET aliases where compatible. Cancellation of legacy bookings not yet in the ledger requires a read/preflight to establish the match identity and facility before mutating. Unknown/missing/malformed reads do not establish “already cancelled.” Do not silently change a cancellation to another booking, account or facility.

All routes except legacy liveness/version exemptions remain internal bearer-authenticated. Operation UUIDs are not credentials. The current single-owner shared token grants broad trust; an actor/reason field is audit metadata, not proof of end-user authority. Management must authorize its own callers and risk policy. Monitoring credentials cannot access status, reconcile, resolution, or mutations.

### Responses and errors

Use an operation envelope containing `operation_id`, kind, state, phase, revision, outcome category, known provider identifiers, warnings, and a status URL. Preserve the original successful creation result; expose later reconciliation/current provider state separately. Proposed mapping:

- `201` completed creation, `200` completed cancellation/no-op or status; replay returns the stored completed operation without executing it.
- `202` accepted/running; `Location` and bounded polling guidance. It is not booking success.
- `409 operation_outcome_unknown` for an accepted execution whose effect is uncertain, with operation ID/evidence; status GET remains `200` with that state.
- `409 booking_conflict`, `account_blocked`, or `idempotency_key_conflict` for their respective conditions; identify relevant conflicting operations to the authenticated caller.
- `400` malformed requests/missing keys; `422` recognized no-effect provider rejection with a stable code such as `credential_rejected` or `booking_rejected`.
- `503 dependency_unavailable`/capacity rejection before admission, with explicit acceptance information. A server cannot declare `not_accepted` when the database commit/result is uncertain.
- `410 operation_result_expired` for a compacted record whose full result has expired; include retained summary and prohibit execution. Never use `404` for a known tombstone.

Status/result replay does not require a currently valid provider password; mutation request structure still requires credentials. No HTTP status alone proves whether external work occurred. Unrecognized response shapes and transport loss at either HTTP boundary are ambiguous; callers use the durable key to inspect acceptance/outcome.

Do not enable a legacy keyless mutation escape hatch. New booking binaries reject v1 POST/DELETE without contacting the provider (e.g. `409 booking_contract_upgrade_required`). Production cutover requires the separately upgraded management client; implementation can land beforehand but must not be auto-promoted into active booking traffic.

## 6. Execution state machine and crash boundaries

Separate the **operation outcome** from its **last known checkout phase**. A successful reservation followed by unknown payment is not a no-effect failure.

| State | Meaning / permissible behavior |
|---|---|
| `accepted` / `running` | Durable claims, one executor; replay observes only. |
| `not_dispatched` | Executor stopped before any booking mutation dispatch intent; no provider booking effect could have been sent by this operation. Terminal for this key. |
| `rejected_no_effect` | Recognized, positively established rejection with no reservation/cancellation effect. Terminal; a different operation may try an alternative account as caller policy permits. |
| `succeeded` | Validated creation/cancellation evidence. Creation can carry an absent-match-ID warning. Terminal effect; later provider changes are separate evidence, not execution permission. |
| `outcome_unknown` | Mutation dispatch may have happened, or a reservation exists but payment is uncertain. Keep all known IDs and blockers; reconciliation may refine the outcome. Never blindly repeat. |

Persist **dispatch intent before every booking/payment-related external mutation**, then response evidence before advancing. Each phase's database transition checks the active executor incarnation/revision and admission state; stale ownership cannot authorize a later phase. This is database fencing only, not fencing of an already sent provider request. A crash between committed dispatch intent and actual network send must be treated as unknown; the ledger cannot prove that no request left. Record attempt number and safe-retry classification for the existing narrowly justified authentication retry. No “intent lease expired, try again” path.

### Create flow

1. Under account admission/coordination, acquire/login to the selected session. Positively identified invalid credentials before reserve are `rejected_no_effect`; an authentication transport outage before reserve is `not_dispatched`, not a bad password.
2. Persist reserve intent, send once, validate a recognized response and required reservation/payment IDs, persist them immediately. Missing IDs, response loss, unexpected HTML/JSON/HTTP errors after possible dispatch are unknown unless a specifically tested no-effect boundary applies.
3. Persist pay intent, send once, validate the recognized acknowledgment and persist payment evidence. Reservation evidence survives every payment failure. Never restart reserve or repeat payment because an HTTP response is ambiguous.
4. Attempt match attachment once with its own intent/evidence. Preserve successful booking evidence if attachment fails, times out, or produces an unrecognized result. Do not rerun session-dependent attachment on restart. Persist an optional match ID and warning.
5. Treat fee/checkout tracking as bounded best-effort adjuncts. Their failure does not erase a successful booking or trigger checkout replay; do not background-retry tracking after restart. Discard a suspect session. Where account-wide checkout interference remains uncertain, retain a separately reported account quarantine even if the booking effect is known successful; do not mislabel that paid booking as failed/unknown just to make it eligible for a replacement.
6. Commit terminal result and durable events before returning success. If finalization fails, stop further external mutations; status may remain unknown despite a real external booking. Retain bounded in-memory evidence long enough to retry **database finalization only**, conditional on the same execution revision.

Current HTTP-200/non-HTML payment handling is insufficient validation. Implement only success shapes supported by reviewed sanitized fixtures/evidence; distinguish malformed/missing data and partial GraphQL errors. A synthetic `{}` fixture does not independently prove settlement. When payment success cannot be established, retain reservation/payment identifiers and expose uncertainty rather than claim successful settlement. Do not invent a payment-status API to make the state machine look complete.

Audit current auth-shaped GraphQL/HTML retry heuristics. Preserve only their demonstrated no-effect cases; a generic HTML page or redirect is not itself proof that a mutation never ran. Classify uncertain variants conservatively and update regression expectations explicitly. Never forward the internal idempotency header to Eversports or attach it as a claim of provider support. Inspect Go transport/proxy redirect and retry behavior, including replayable bodies and `Idempotency-Key` handling; prove upstream mutation dispatch counts with tests.

### Cancellation

Persist target/claim and cancel intent, then validate a returned matching ID and confirmed cancelled state. Known already-cancelled state is success without mutation, not a generic “not found means success.” A documented no-effect business refusal is terminal rejection; malformed/partial result or lost response is unknown. A later authoritative cancelled-state read can establish the desired outcome, but need not establish which actor cancelled it or prove a refund amount. No automatic repeated cancellation or automatic cancellation of a suspected duplicate creation.

### Recovery

On startup, after exclusive process admission and the previous process stopping, inspect unfinished operations of the previous incarnation and **derive the outcome from durable evidence**:

- Validated reservation **and payment** success, or confirmed cancellation, already journaled: finalize `succeeded` without another provider call; an unfinished optional attachment becomes a warning. Retain any separate account/session quarantine.
- At least one mutation intent exists and every attempt has durable, positively established no-effect rejection evidence: finalize `rejected_no_effect`.
- No mutation dispatch intent exists: finalize `not_dispatched`.
- Any required effect is still uncertain: mark `outcome_unknown`, keeping identifiers/claims.

A crash after payment evidence is committed must not turn a known paid booking into an unknown operation eligible for duplicate-risk creation. Recovery never dispatches old mutations. Read-only reconciliation is caller-triggered with credentials; no stored password means no autonomous authenticated crash-recovery worker.

A valid unknown operation can be inspected/reconciled repeatedly. Refined status has a new revision and provenance; old responses/journal entries remain auditable. Late executor evidence may be conditionally appended, but a stale executor cannot regain ownership, clear a replacement's claims or overwrite newer decisions.

## 7. Account, session and singleton coordination

The first release stays single-active but must handle concurrent HTTP requests, duplicate management credential entities and process replacement correctly.

- Initially allow one executing booking process overall in this ledger/deployment; concurrent additional facility pools are not enabled by this task. Acquire a dedicated PostgreSQL session-level singleton guard for that execution domain before admitting mutations. This is a fail-closed guardrail, not multi-active support. Keep it distinct from migration and discovery locks; a second process is observable but not ready for mutation.
- On guard-connection loss, stop admission, cancel local executor contexts and do not reacquire execution ownership in the same incarnation. Drain/stop; unresolved database evidence survives. A released DB lock does **not** fence Eversports or prove a paused/dead worker's request stopped.
- Stop-before-start remains the operational rule. Do not automatically take over a prior incarnation's active/unknown account operation because a TTL, lock or discovery lease expired. Positive process stop plus recovery classification precedes replacement. An uncertain DB disconnect/process pause cannot grant duplicate-free failover.
- Use durable account ownership plus context-aware local coordination independent of the cache key. Acquire it before login and hold through all session-dependent checkout work. Conservatively serialize authenticated reads/session refresh against active checkout on that account; status-from-ledger needs no session lock.
- Unresolved account checkout state blocks ordinary later mutations using that account, even at another court/time. Read-only reconciliation with a fresh isolated session remains possible after the old executor stops. A risk-authorized replacement can bypass only its explicitly referenced lineage's uncertainty, never an unrelated active/unknown operation.
- Same login in different groups/credential rows shares coordination; changing password cannot bypass it. Map known provider-user aliases before mutation when the login response supports it.
- Bound cache size and idle lifetime; atomically construct clients and pin entries while in use so eviction cannot create concurrent session/lock domains. Start with 32 entries/15-minute idle eviction, configurable later if necessary. Cache loss is a performance event only. Rotation removes obsolete idle clients without disrupting an active checkout.
- Reuse a bounded HTTP transport, disable unsafe redirects, and implement operation-specific safe retry rules, not generic mutation middleware. Bound per-account/global concurrency and request rates; return capacity rejection before admission instead of building a durable queue. Begin with one external mutation flow at a time; increasing throughput is a separate measured decision.

Manual Eversports browser operations and unrecognized account aliases remain outside these controls. In particular, another login may invalidate a session or modify account-wide checkout state. Do not claim application locks prevent this.

## 8. Controlled duplicate-risk creation

A replacement request uses a new key and immutable fields such as `replaces_operation_id`, `accept_duplicate_risk: true`, and a bounded reason. These names are illustrative; the typed contract must reject unrecognized/contradictory options.

Admission requires all of the following, checked atomically with the new operation/claims:

1. The authenticated caller explicitly requests creation, not cancellation or a generic reexecute action.
2. The parent is an existing **unknown creation** with a stopped executor. An HTTP timeout while it is still running is insufficient. A known success/rejection is not eligible for this bypass.
3. Provider/facility/court/time match the parent. A different account is allowed only as an explicit immutable field of the new operation, never implicit credential rotation. The old account's unknown state remains recorded.
4. No unrelated active/conflicting operations are bypassed, and no known active local checkout can overlap the replacement.
5. Exactly one replacement child per parent, plus an initial **one-risk-replacement-per-root** budget, enforced durably under concurrent requests. This proposed conservative limit prevents an automatic replacement chain; a larger policy would require an explicit contract change.
6. Record parent, root, caller provenance/reason, risk acknowledgment and acceptance revision before dispatch. Lost HTTP responses cannot authorize a second child.

Use a fresh session for the replacement. This reduces reuse of uncertain checkout state but cannot guarantee account/provider isolation. The parent remains unknown even if the child succeeds; retain both and notify the caller of possible duplicates. New conflicting work remains blocked by unresolved ancestor claims until evidence resolves them. No automatic compensating cancellation, and no API for deleting a ledger entry to bypass this policy.

If read-only reconciliation first proves the parent succeeded, return that evidence and do not admit a risk replacement. A later discovery that the parent succeeded after child admission records both effects; it cannot retrospectively undo the authorized risk.

## 9. Reconciliation, retention and security

Use existing slot ownership/match reads only to the extent their response proves something. A slot occupied by someone, a missing slot, an empty match ID, or a booking created manually by the same account does not unambiguously identify this operation. A read must match known provider IDs, scope and time; conflicting candidates remain unknown. Provider eventual visibility has no established upper bound here, so “not found after N seconds” is not proof of no effect.

Resolution actions record evidence and expected revision, are idempotent, and cannot reset execution history. Legacy imports use the same key/fingerprint and conditional-evidence rules, but identify their records as imported rather than inventing a successful historical create operation. Conflicting imports/known claims are surfaced, not overwritten; missing or ambiguous provider proof stays unresolved and is not silently imported as confirmed. No import/resolution path dispatches reserve, pay, attach or cancel. Verified known-booking or cancellation evidence can update resource claims transactionally. A human attestation is labeled as such, not disguised as provider proof; evidence-free no-effect assertions cannot silently unlock a new ordinary booking. The explicit creation-risk path is the supported way to accept unresolved risk. An internal API/runbook is sufficient initially; a web/Telegram resolution UI is deferred.

Proposed retention defaults:

- Retain full completed result/evidence for 90 days after the later of terminal completion and the booking end time.
- Never compact unresolved operations, active claims, or evidence still needed by an unresolved replacement lineage just because time passed.
- Preserve compact operation tombstones/lineage indefinitely initially, including expired-result status. Storage is bounded per operation, not by silently forgetting keys; monitor total growth and revisit archive policy explicitly.
- No table/schema drops or dedup reset on deploy/rollback, DB restore, credential deletion or service-key changes. Management must preserve access to credentials needed for unresolved/active bookings under its separate deletion rules.

Provider responses are untrusted data. Bound requests, responses, identifiers and reasons; validate required IDs/state; avoid raw upstream body snippets in public errors or durable evidence. Logs use operation IDs and phase/outcome, never passwords, cookies, login strings, raw request bodies or DSNs. HMAC account IDs and operation IDs are not metric labels. Store only minimal personal/provider data; protect the shared DB/backups with the existing owner-operated trust boundary.

## 10. Bounded execution and discovery foundation

### Initial timing defaults to validate

| Budget | Proposed value / behavior |
|---|---|
| HTTP wait for a newly accepted operation | Up to 20 seconds, then `202`; caller disconnect ends waiting only. |
| Total service-owned execution | 90 seconds maximum, including login and optional adjuncts. Bound optional work separately so it cannot consume all finalization time. |
| Individual provider call | At most 20 seconds and never beyond the remaining execution budget. |
| Durable finalization after cancellation/deadline | Separate bounded 5-second database-only context; if unsuccessful, recovery reports uncertainty. |
| Graceful shutdown | 100 seconds, with Compose stop grace at least 110 seconds; drain first, then cancel at budget and finalize. |
| Admission wait | Short bounded wait (initially 1 second), otherwise reject before acceptance; not an offline queue. |

Use injected clocks/deadlines in tests; these are implementation defaults, not evidence of upstream response time. Create a fresh execution context carrying only sanitized metadata, not all request values/credentials indefinitely. Track executors and their credentials in memory only for their bounded lifetime. Shutdown must account for accepted work whose HTTP handler already returned `202`.

Add reusable `internal/observability`/lifecycle pieces rather than a second incompatible framework. Booking preparation provides:

- Explicit validated facility ID/UUID/slug and squash scope; remove reliance on an implicit default facility UUID for mutations. Config consistency checks are local; they do not prove that arbitrary supplied provider IDs refer to one real facility.
- Diagnostic contract metadata for v2 and supported read aliases, fixed capability vocabulary including operation status/reconciliation, and an immutable descriptor builder/lifecycle attachment seam. No registration goroutines or management dependency yet.
- Internal admin listener with `/health/live`, `/health/ready`, protected `/metrics`, and diagnostic `/version`; preserve `/health` as liveness alias. Readiness requires compatible booking schema, DB access, singleton admission and initialized API, and is false while draining. Individual unknown accounts are reported separately rather than making every account unavailable.
- Official Prometheus Go collectors and bounded route/operation/outcome metrics, compatible with discovery §11. Expose durable unknown-operation counts/age, database/admission health and risk-replacement counts; never account/game/operation IDs as labels. Replays do not increment external-execution counters.
- Monitoring-only bearer authorization and no public admin ports. Registry absence never controls booking mutation admission; DB/operation rules do.
- Shutdown hooks ordered so a future registrar reports draining before admission closes and remains best-effort. Keep admin observable while executors finish. Future registration failure/lease expiry does not release business claims.

A direct authenticated client using an explicitly configured address can use booking with management stopped. A registry-only client still cannot resolve new addresses without the registry; do not add a silent legacy-URL bypass to discovery mode.

## 11. Implementation work packages

Each package is a bounded local task with a reviewable diff. Do not promote/deploy a partly integrated mutation contract. P1–P7 change booking/shared infrastructure only; M is separate management work. **Progress:** P1 and P2 are committed; P3 is implemented locally. None is wired to a runtime v2 route; P4 and P5 must supply journaled provider phases, recovery and handler semantics first.

| Package | Changes / likely locations | Acceptance gate |
|---|---|---|
| **P1 — contract and failure model (implemented)** | `internal/bookingapi` v2 DTO/error constants, UUID/scope/credential/time/risk validation, HMAC account identity and deterministic password-independent request fingerprints; `eversports` typed phase/effect/auth failures plus reserve/payment/cancel response validation. No handler wiring. | Focused contract/provider tests pass; existing no-ambiguous-retry fixtures remain green; no provider calls in tests. |
| **P2 — booking persistence (implemented; runtime wiring deferred)** | `cmd/booking/storage`, separate embedded migrations/history, bounded pool and identity-key marker handling; repositories for operations, steps, accounts, claims, evidence, events and tombstone markers. `SetupEmptyTestDB` supports independent stream testing. | Real disposable PostgreSQL tests cover sequential/concurrent key admission, overlap/cancellation claims, concurrent replacement budget, both migration orders and booking-only admission. Repository admission returns success only after commit; P3 queries the same key after a lost acknowledgment, while production provider dispatch remains unwired. |
| **P3 — execution coordinator (implemented locally; runtime wiring deferred)** | `cmd/booking/service` provides repository/runner seams, operation-key admission gates, one tracked provider flow, caller-independent execution deadlines and bounded account-scoped session leases. `cmd/booking/storage/singleton.go` owns a dedicated PostgreSQL session lock and permanently reports authority loss without reacquisition. | Unit/race tests cover concurrent replay, uncertain admission commit acknowledgment, pre-admission capacity/session failure, caller cancellation, draining, guard-loss cancellation, restart with rotated credentials, account serialization and cache rotation/eviction. PostgreSQL integration covers singleton exclusion and connection loss. |
| **P4 — journaled provider flows** | Refactor `checkout.go`/`matches.go` into phase-aware operations without moving HTTP parsing into SQL repositories; persist each intent/evidence before proceeding; recognized success validation; cancellation/no-op and optional match semantics. | Fault injection at every send/response/persistence boundary; known IDs survive; payment/cancel ambiguity never restarts external work; post-payment optional failure retains successful evidence. |
| **P5 — v2 API and recovery controls** | Booking handlers/client seams; status, read-only reconcile, idempotent evidence resolution/import; controlled replacement admission; keyless v1 mutation rejection; retention compaction. | Contract HTTP tests use fake provider plus real ledger where relevant; stopped-unknown-only replacement and root budget enforced; replay with rotated password has no provider call; expired keys cannot execute. |
| **P6 — lifecycle/observability foundation** | Booking `main.go`, `server.go`, `internal/config`, reusable admin/metrics helpers and descriptor seam; explicit facility configuration, bounded shutdown, Compose DB/timing/admin wiring. No real registrar. | Management absent: direct fake-provider booking still works; DB absent: no new mutation; caller disconnect/202 does not orphan executor tracking; draining stops admission and preserves evidence; protected admin metrics. |
| **P7 — operational handoff and integration gate** | Operator/service references, architecture/invariants/README affected sections, test helpers/check scripts, backup/restore and cutover runbook. Keep proposal status honest. | Full applicable verification; stop/start and DB-loss drills with fake provider; rollback never reopens keyless traffic; management compatibility gate documented and enforced in rollout. |
| **M — management integration (separate task)** | Requirements and test matrix in [discovery §10.1](service-discovery-design.md#101-management-integration-with-the-reliable-booking-contract); not included in P1–P7. | Durable business intent/replay/risk policy, partial evidence persistence, cancellation semantics and v2 contract support pass before production mutations switch. |
| **D — later discovery attachment** | Shared registrar/lifecycle wiring, real instance descriptors/heartbeats/draining, v2 resolution and catalog metadata. | Registration outage does not disable otherwise healthy direct booking; discovery leases never become execution locks; production singleton guard remains. |

Dependency order: P1 → P2 → P3 → P4 → P5; P6 can develop alongside P2–P5 once contracts are fixed; P7 needs all of them. M can be implemented against P1 fixtures independently. D follows the shared discovery foundation, not vice versa. Neither P7 nor D enables multiple active booking replicas.

## 12. Acceptance and fault-injection matrix

Use local `httptest` provider fixtures, injected clocks/transport barriers, and disposable PostgreSQL. No live accounts, bookings, cancellations, production DB or `.env` during verification.

| Area | Required assertions |
|---|---|
| API deduplication | Sequential/concurrent repeats, response loss, process restart, fresh cache, same key at another instance/address: one permitted execution, same recorded outcome/status. Different body/scope/account under one key: conflict before provider calls. |
| Transport behavior | Client→booking duplicate keyed calls are deduplicated; upstream redirects, replayable POST bodies, connection reuse failure, 5xx/HTML/partial JSON do not cause unapproved repeated mutation. Count reserve/pay/attach/cancel calls separately. |
| Crash boundaries | Kill executor before admission commit, after commit, before/after every dispatch intent/send/response/evidence commit, and after final result before response. Recover `not_dispatched` only with no possible send; derive confirmed success/rejection from durable evidence, and retain uncertainty/identifiers for the remaining cases. |
| Database faults | Read/claim/commit/finalization loss, singleton connection loss and full DB outage; no in-memory fallback, stale worker transition or TTL-based takeover. Recovery can refine evidence without repeating provider mutation. |
| Account/session safety | Same credentials through different group/venue entities; same login different password; concurrent first cache access; reads/refresh during checkout; eviction; recognized provider alias. No cache-dependent lock separation. |
| Business conflicts | Same/overlapping intervals across accounts/groups, different non-overlapping intervals, different facility/court, repeated cancellation target; known cancellation releases only its own claim; create replay after cancel never rebooks. |
| Risk replacement | Ordinary new key blocked by unknown; explicit new child accepted only after parent executor stops; same-child replay safe; two distinct concurrent child requests admit one; no grandchildren/root-budget bypass; account changes explicit; parent uncertainty and other blockers retained. |
| Cancellation | Already-cancelled positive read, wrong/missing target, denied cancellation, ambiguous response, local persistence failure after provider success; no automatic repeat or inferred refund success. |
| Evidence-based recovery | Payment/cancellation success journaled before crash finalizes success locally; unfinished optional attachment is a warning, not replacement eligibility. Known no-effect evidence finalizes rejection; unknown required effects remain blocked. Account quarantine and booking outcome are distinct. |
| Partial creation | Reserve succeeded/payment unknown persists all IDs; recognized paid booking/attachment failure retains result with missing match ID; finalization failure cannot turn an existing booking into no-effect failure. |
| Reconciliation | Positive exact-ID evidence refines outcome; delayed visibility, no match ID, empty reads, another owner's booking and multiple/manual candidates remain uncertain. Repeated imports/resolution keys cannot duplicate claims or falsify legacy provenance; conflicts remain explicit. No reserve/pay/attach/cancel calls from reconcile/resolution/import. |
| Migration/retention | Separate version tables and lock behavior in the same schema; both stream orders; concurrent startup; cleanup includes booking tables intentionally; compaction preserves old-key conflicts and active/unresolved evidence; stale backup restoration requires operator gate. |
| Independence/lifecycle | Management never started, DB present; DB missing/recovering; 202/disconnect; bounded shutdown; second singleton rejected; no new operation admitted while draining; already accepted work finishes within budget, and no later phase starts after execution authority is lost; registry absent does not affect direct operation. |
| Security/metrics | Auth required on operation APIs; monitoring token cannot mutate/read business evidence; request/result/log redaction; bounded unknown routes/labels; replay and risk counters mean what they claim. |

Focused existing command: `go test -count=1 -timeout 120s ./cmd/booking/...`. New integration packages must join the existing integration-tagged suite and fail explicitly without Docker. Run `make check-fast` during implementation and `make check` for final executable changes, including race and service/database lifecycle coverage; follow [development](development.md) for prerequisites and separate security checks. Tests of fake provider behavior do not establish an undocumented upstream guarantee.

## 13. Cutover, rollback and remaining risks

1. Land additive contracts/storage/tests locally in reviewed packages. No service version bumps or automatic release/promotion as part of agent work.
2. Complete M against the same fixtures. Old management must not operate a new keyless-blocking booking release in production; new management must refuse unknown/v1 mutation semantics rather than fall back.
3. Schedule a coordinated, owner-authorized mutation pause; stop old callers/executors. Check in-flight external outcomes before bringing up the new booking executor. Existing court-booking records alone may omit partial bookings: inspect/import verified active provider evidence via the new API and record unresolved gaps, rather than assume an empty ledger means an empty facility.
4. Apply booking migrations and preserve management data. Configure DB/identity key/admin token and validated facility; run fake-provider migration/restart drills first. No claim that pre-cutover requests had idempotency keys or retrospective deduplication guarantees.
5. Bring up one booking instance and upgraded management, initially using static invocation. Verify contract negotiation, keyed status/replay, partial-result handling, caller risk-policy audit and bounded shutdown before enabling scheduled mutations.
6. Attach real discovery later; advertise v2 mutations/status only when implemented. Do not publish a v1 mutation capability as interchangeable with v2. Discovery cutover and reliability cutover need not occur together.
7. Roll back discovery routing to static **without** rolling back the reliable booking contract. If application rollback requires a keyless booking binary/client, keep mutations disabled; retain status/evidence access through a compatible binary and resolve outstanding operations before any separately reviewed legacy resumption. Never drop booking tables to make rollback convenient.

Open evidence limitations, not unanswered product decisions:

- Eversports has no documented idempotency/settlement/reconciliation contract available for this plan. Unrecognized outcomes remain unknown. Better automatic recovery requires separately obtained sanitized evidence or explicit authorization for provider investigation; it is not a prerequisite to implementing conservative durable admission.
- Login/session isolation, alternate account aliases and manual browser interference cannot be proven with application locks. A fresh session is not fencing.
- An explicit risk replacement may produce two reservations/charges, even with one local executor. That is the accepted, observable exceptional risk, not an exactly-once guarantee.
- Shared database/schema/possibly role retain a common availability and coarse security boundary. Booking is independent of the management process, not independent infrastructure.
- Unknown account/resource claims may remain blocked indefinitely. Caller-authorized creation replacement mitigates missed booking risk only within its stated scope/budget; it does not establish general automatic recovery or safe cancellation replay.
- Multi-active booking requires a separate proof for account/worker ownership under partitions, provider limits and session behavior. Discovery TTLs and database locks alone do not supply that proof.

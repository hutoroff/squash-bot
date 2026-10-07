# Management service

Owns business rules, PostgreSQL persistence/migrations, scheduled work, and server-owner decisions. [main.go](../../cmd/management/main.go) is the wiring source; read [scheduling and booking](management-scheduling.md) only for those workflows.

## Where to look

| Change | Source |
|---|---|
| Routes and HTTP middleware | [api/server.go](../../cmd/management/api/server.go), domain handler files beside it |
| Repository/Telegram contracts | [service/interfaces.go](../../cmd/management/service/interfaces.go) |
| External booking contract | [service/booking_client.go](../../cmd/management/service/booking_client.go) |
| Durable booking business/operation intent foundation | [service/booking_intent_service.go](../../cmd/management/service/booking_intent_service.go), [storage/booking_intent_repo.go](../../cmd/management/storage/booking_intent_repo.go) |
| Game creation/publication/manual booking | [service/game_service.go](../../cmd/management/service/game_service.go) |
| Participation and announcement edits | [participation_service.go](../../cmd/management/service/participation_service.go), [game_notifier.go](../../cmd/management/service/game_notifier.go) |
| Venues and credentials | [venue_service.go](../../cmd/management/service/venue_service.go), [venue_credential_service.go](../../cmd/management/service/venue_credential_service.go), [encryptor.go](../../cmd/management/service/encryptor.go) |
| Canonical identities and owner roles | [api/users.go](../../cmd/management/api/users.go), [storage/user_repo.go](../../cmd/management/storage/user_repo.go) |
| Result decisions and ratings | [game_result_service.go](../../cmd/management/service/game_result_service.go), [rating_service.go](../../cmd/management/service/rating_service.go) |
| Schema and migrations | [migrations](../../migrations), [storage](../../cmd/management/storage) |

Use current signatures/routes from those files, not a copied inventory. New business behavior belongs in services; persistence belongs in repositories. Some current API handlers use concrete storage types and result/rating code uses pgx transactions/storage errors; see [boundary exceptions](../architecture.md#package-boundaries).

## Identity, authorization, and audit

- Resolve provider identities once at the transport boundary with `/api/v1/identities/resolve`; user actions carry canonical `user_id`, while game roster/result references use `player_id`. `players` remains lazy, so an authenticated user may have no player row.
- `ResolveIdentity` updates supplied profile fields, including cleared values. `EnsureIdentity` is seed-only. Do not use seed behavior to retain stale Telegram profiles.
- Server-owner checks read the DB; `SERVICE_ADMIN_IDS` is a grant-only startup seed. Last-owner revocation is guarded transactionally in `storage/user_repo.go`.
- [AdminGroupsResolver](../../cmd/management/service/admin_groups_resolver.go) translates canonical user ID to Telegram ID. It caches successful resolutions and coalesces concurrent misses; do not assume role changes have zero cache delay.
- Internal bearer authentication is a trusted-transport boundary, not proof that arbitrary user-supplied actor headers are authentic. Web/bot must establish caller identity and group access before invoking privileged actions.
- [AuditService](../../cmd/management/service/audit_service.go) is best-effort: logging errors must not fail the business operation. Existing handlers often omit user audit when actor ID is zero; some system operations have explicit system events. Follow the relevant event's semantics rather than applying a blanket zero-actor rule.
- Mutating actor fields are overwritten from authenticated transport identity, not trusted from client bodies. Audit visibility is filtered by management, with owner authority read live.
- Event strings live in [audit_event.go](../../internal/models/audit_event.go). When changing them update frontend types/labels and run [the drift test](../../web/frontend/src/auditEvents.test.ts).

## Games, venues, and credentials

- Venues own per-sport units and overrides; games snapshot sport and resolved players per unit. Preserve `Game.Capacity()` semantics and existing legacy-field compatibility; see [models](../../internal/models).
- `PublishGame` is the publication primitive: reject already-published games, send/pin, persist the message ID, and attempt orphan deletion if that persistence fails. Do not replace ordinary announcement edits with publication.
- `GameNotifier` serializes each fetch/render/edit sequence using process-local locks. Telegram also has a separate coalescer; neither provides cross-process ordering.
- Participation mutates the DB then asynchronously invokes the notifier. Announcement failures are logged, not converted into failed participation responses. Preserve request/context lifetime handling when changing asynchronous work.
- Credential passwords are AES-256-GCM encrypted; do not serialize decrypted credentials or return passwords from list APIs. Missing encryption configuration disables credential operations.
- Venue/credential deletion is blocked only by non-canceled bookings dated today or later in the group's timezone, not every historical row. Storage owns these predicates; tests use real PostgreSQL.
- The Web-only preventive cancellation fraction is `1/3`, `1/2`, or `2/3`. Updates omitting it (including Telegram edits) preserve the stored value.

## Results and ratings

- Results require two players per unit, registered author/opponent, an optional consistent score, opt-out checks, and the per-group-local-calendar result window. The `completed` flag does not alone define eligibility. Scores carry explicit `points`/`games` kind; omitted kind remains unknown for old clients. New scores require the winner's side to be strictly higher or a draw's sides equal; each side is limited to six decimal digits. Winner membership is validated even when the score is skipped.
- Opponent approval and timed auto-approval commit status and Glicko-2 changes in the **same transaction**. On rating failure, do not leave the result decided without its rating changes.
- Rating rows are locked in player-ID order to avoid deadlocks, including the re-read after a concurrent default-row insertion. Preserve the ordered critical section and rating/history atomicity.
- The default-disabled `rating.score_aware` flag is resolved once inside the approval transaction for both players. Lookup failure aborts approval, not a silent fallback. Valid typed decisive scores use bounded evidence weighting; disabled/skipped/draw/legacy cases use ordinary Glicko-2. History records version, weight, kind, effective enabled state, and selection reason. See [feature toggles](../feature-toggles.md) for formula, limitations, precedence, and activation semantics.
- Unit-test/disabled-rating wiring can use the non-transactional fallback. Tests of that fallback do not prove the production transaction path works.
- User notification/audit work is best-effort and not a guarantee of delivery or a fully transactional audit log.

## Feature flags

Management owns persisted global/group overrides, live server-owner authorization, and best-effort change audit. Shared definitions live in `internal/featureflags`; unset defaults are always disabled. Existing booking and notification settings are not migrated. The canonical inventory and mandatory maintenance instructions are in [feature toggles](../feature-toggles.md).

## Reliable booking caller integration

Migration 039 adds management-owned `management_booking_intents` and `management_booking_operations`, plus additive origin columns on `court_bookings`. A stable request key protects the high-level requested-count decision; each selected create/cancel has a UUIDv7 operation key, immutable fingerprint, credential reference, facility/provider origin, local date/time, execution generation, bounded status/evidence projection, and optional duplicate-risk lineage. Concurrent same-key admission returns one stored intent/operation; changed reuse conflicts. This state is separate from booking's `booking_*` ledger and management never writes that ledger. No database triggers or stored procedures implement business policy.

When booking is configured, startup requires `BOOKING_RELIABLE_CUTOVER_ACK=keyed-v2-only` and matching service/facility scope. Every operation verifies booking's bearer-authenticated v2 descriptor, required capabilities, contract major, and exact scope before mutation/status traffic; mismatch fails closed. Scheduled auto-booking, scheduler cancellation, Telegram manual booking, and Telegram manual removal/cancellation use keyed v2. The production read adapter fails closed before HTTP if a keyless mutation method is reached. Accepted/running work is observed within a bounded foreground budget and by a coalesced periodic recovery pass; only planned records may resubmit the same immutable key/payload. Court cancellation requires the original persisted binding and fails closed after a configuration-origin mismatch.

Unresolved planned/accepted/running/unknown operations protect their credential and venue even when their game date is historical; terminal history does not permanently prevent deletion. Bearer-authenticated internal endpoints expose sanitized local operation inspection (`GET /internal/booking/operations/{id}`), durable aggregate metrics (`GET /internal/booking/metrics`), credentialed reconciliation (`POST .../{id}/reconcile`), and keyed verified resolution (`POST .../{id}/resolution`). Explicit duplicate-risk allocation exists as an unexposed policy primitive with a one-child root budget; ordinary automation cannot invoke it. Durable state changes enqueue bounded at-most-once administrator DMs for unknown, partial, possible-duplicate, warning, and missed-action conditions; claims commit before Telegram calls so restarts cannot create notification loops.

Reliable-booking production cutover remains blocked pending combined-candidate acceptance, legacy inventory/import, and the runbook drill. The management admin listener starts before database/migration and Telegram initialization, reports unready until migrations, a live database, and the business listener are available, and exposes monitoring-token-protected build/descriptor identity plus bounded Prometheus process/service metrics. Shared discovery contracts, static resolution, and the management-owned lease registry are implemented. Migration 040 stores durable catalog identities and bounded process memberships. Internal bearer-authenticated registration, heartbeat, drain, deregistration, resolution, and catalog routes validate immutable logical scope plus approved private networks/ports. Self-registration remains independent of invocation. With `DISCOVERY_MODE=registry`, management constructs its reliable booking clients from the management-local registry resolver even when no booking instance exists at startup. Resolution requires the configured logical key, exact facility scope, booking v2 contract, and all read/mutation/status capabilities; zero or multiple active targets fail before dispatch, and static URLs are not fallback targets.

## Bounded request and dependency metrics

The protected admin `/metrics` exports `squash_http_requests_total` and `squash_http_request_duration_seconds` for management business HTTP requests using registered method-qualified mux templates (including authorization failures), with unmatched routes and unsupported methods normalized. `squash_dependency_requests_total` and `squash_dependency_request_duration_seconds` use the fixed `booking` target and bounded HTTP status/error outcomes for booking reads, descriptor checks, and keyed v2 requests; registry failures before dispatch are counted separately from actual dispatches. A `202` response means accepted, not a completed booking. These metrics do not count provider executions; booking's own `booking_external_executions_total` retains that role. No raw URL, path, scope, credentials, operation ID or response body is a label.

## Durable booking monitoring

The protected admin scrape reads `BookingIntentRepo.GetMetrics` with a one-second budget, independently of the JSON booking-metrics endpoint. It exports fixed-state gauges for planned/accepted/running/unknown/succeeded/rejected-no-effect operations, pending notifications, linked duplicate-risk allocations, and the oldest unresolved operation's age. Repeated same-key status reads and restarts cannot increment an execution counter: these values come from durable rows each scrape. On database failure it emits storage-health=0 and omits aggregate gauges rather than serving stale values. There are no operation, group, account, or provider identifiers in labels. Scheduler outcome/last-completion limits are described in [scheduling](management-scheduling.md#jobs-and-timing).

## Registry inventory health

`MONITORED_SERVICE_KEYS` is a bounded operator-owned JSON array of expected singleton bindings. Each entry contains `service_key`, `contract`, `major`, optional required `capabilities`, and optional exact `scope`; duplicate keys and invalid selectors are rejected at configuration validation. The management admin metrics export expected=1 for every configured key, eligible ready live counts, and registry-storage health. A storage failure exports storage health zero and expected inventory only; it does not report stale live counts. When booking is configured, management automatically appends its keyed v2 binding with the exact configured facility scope and required capabilities if it is not already present in the inventory.

## Monitoring service discovery

`GET /internal/discovery/v1/prometheus-targets` is a monitoring-token-only Prometheus HTTP service-discovery endpoint on the management business listener. It returns unexpired, non-terminal non-management memberships (including starting, not-ready, ready, and draining) as registered admin host/port targets with only `service_key`, `service_kind`, and state labels. The metrics path is fixed by monitoring configuration, not a service descriptor. It returns `200 []` only for an empty snapshot and `503` for registry storage failures; the internal API secret cannot access it.

## Booking availability

`GET /api/v1/venues/{id}/booking-readiness?group_id={id}` remains a non-mutating readiness check. After group, venue, and usable-credential checks, it verifies the configured keyed booking binding without provider credentials or a booking operation. A missing/unreachable target returns `booking_service_unavailable`; a contract/scope/capability mismatch returns `booking_contract_incompatible`; and more than one active target returns `booking_service_ambiguous`. These are stable user-facing reasons, not transport errors, and return `ready: false` with zero capacity.

## Persistence and tests

Use new migration files for schema changes; do not edit already-applied migrations to retrofit behavior. Inspect all ordered migrations when reasoning about schema. The former `auto_booking_games_count` was replaced by `auto_booking_courts_count`; it is not a current column.

Integration fixtures use [SetupTestDB, Truncate, and CreateTestUser](../../internal/testutil/testdb.go). There is no `TruncateTables` helper. Fixtures must create canonical identities and satisfy current foreign keys; the maintained service/database lifecycle test demonstrates current constructors and identity contracts.

Start with focused service/API tests, then integration tests for persistence/transactions. [Invariants](../invariants.md) identifies nearby tests and gaps. Explicitly requested Docker-backed suites fail when Docker is unavailable; [Development](../development.md) documents the unified commands and diagnostics. Configuration defaults live in [config.go](../../internal/config/config.go), operator descriptions in the [README](../../README.md#environment-variables).

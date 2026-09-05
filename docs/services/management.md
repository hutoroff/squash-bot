# Management service

Owns business rules, PostgreSQL persistence/migrations, scheduled work, and server-owner decisions. [main.go](../../cmd/management/main.go) is the wiring source; read [scheduling and booking](management-scheduling.md) only for those workflows.

## Where to look

| Change | Source |
|---|---|
| Routes and HTTP middleware | [api/server.go](../../cmd/management/api/server.go), domain handler files beside it |
| Repository/Telegram contracts | [service/interfaces.go](../../cmd/management/service/interfaces.go) |
| External booking contract | [service/booking_client.go](../../cmd/management/service/booking_client.go) |
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

- Results require two players per unit, registered author/opponent, a supported score, opt-out checks, and the per-group-local-calendar result window. The `completed` flag does not alone define eligibility.
- Opponent approval and timed auto-approval commit status and Glicko-2 changes in the **same transaction**. On rating failure, do not leave the result decided without its rating changes.
- Rating rows are locked in player-ID order to avoid deadlocks. Preserve the ordered critical section and rating/history atomicity.
- Unit-test/disabled-rating wiring can use the non-transactional fallback. Tests of that fallback do not prove the production transaction path works.
- User notification/audit work is best-effort and not a guarantee of delivery or a fully transactional audit log.

## Persistence and tests

Use new migration files for schema changes; do not edit already-applied migrations to retrofit behavior. Inspect all ordered migrations when reasoning about schema. The former `auto_booking_games_count` was replaced by `auto_booking_courts_count`; it is not a current column.

Integration fixtures use [SetupTestDB, Truncate, and CreateTestUser](../../internal/testutil/testdb.go). There is no `TruncateTables` helper. Fixtures must create canonical identities and satisfy current foreign keys; the maintained service/database lifecycle test demonstrates current constructors and identity contracts.

Start with focused service/API tests, then integration tests for persistence/transactions. [Invariants](../invariants.md) identifies nearby tests and gaps. Explicitly requested Docker-backed suites fail when Docker is unavailable; [Development](../development.md) documents the unified commands and diagnostics. Configuration defaults live in [config.go](../../internal/config/config.go), operator descriptions in the [README](../../README.md#environment-variables).

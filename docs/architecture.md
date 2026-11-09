# Architecture and ownership

## Runtime map

Four independently deployable binaries share one Go module. Entry-point wiring in `cmd/<service>/main.go` is authoritative.

| Binary | Owns | Dependencies |
|---|---|---|
| `management` | Business API, PostgreSQL persistence/migrations, scheduled jobs, server-owner decisions, management-owned booking business intents | PostgreSQL, Telegram Bot API, optional booking service |
| `telegram` | Telegram transport, commands, callbacks, in-memory wizards | Management HTTP API, Telegram Bot API |
| `booking` | Keyed reliable booking API, booking-owned ledger/migrations, singleton execution coordination, bounded in-memory provider sessions, Eversports adapter | PostgreSQL, Eversports |
| `web` | Telegram Login Widget verification, JWT sessions, authorized proxies, embedded SPA | Management HTTP API |

Default business HTTP ports: management 8080, booking 8081, web 8082; booking's authenticated internal admin listener defaults to 9091, and telegram uses 8083 only in webhook mode. Polling telegram does not bind its webhook HTTP server. Public `/health` routes are liveness checks. Booking additionally reports database/singleton/coordinator readiness on its monitoring-authenticated admin listener.

Clients keep startup-configured URLs. [Service discovery](service-discovery-design.md) is proposed, not implemented. Keep one active management/telegram/booking instance; this code does not provide distributed scheduler or wizard coordination. Booking uses PostgreSQL singleton authority plus durable account/resource coordination for one active executor, but this does not authorize multiple active replicas or fence manual Eversports activity.

## Package boundaries

- `cmd/management/api`: HTTP validation/authorization, service calls, responses.
- `cmd/management/service`: business rules, jobs, repository/Telegram interfaces.
- `cmd/management/storage`: pgx SQL repositories, persistence errors, migration/pool support.
- `cmd/telegram/client`: typed management HTTP client and bot-facing interface.
- `cmd/telegram/telegram`: Telegram-specific rendering, routing, and state.
- `cmd/booking/booking`, `cmd/booking/service`, `cmd/booking/storage`, and `cmd/booking/eversports`: business/admin HTTP, reliable orchestration, booking-owned SQL, and upstream adapter respectively.
- `cmd/web/webserver`: web auth and management proxies; no database dependency.

Prefer dependency injection and service-owned interfaces for new work. **Existing exceptions:** management API uses some concrete storage types; `game_result_service.go` imports a storage sentinel error; result/rating services and auto-approval use pgx transactions/pools, and rating SQL exists in the service. Do not describe the current code as strict `api → service ← storage` layering, or refactor away transaction atomicity merely to remove an import. Address boundary changes as explicit tasks with regression tests.

## Shared domain and formatting

- [Models](../internal/models): domain objects, canonical identity, audit event constants.
- [Sport registry](../internal/sport/sport.go): supported sports, unit names, default/max players per unit. Venues store sports/units; games snapshot sport and resolved `players_per_court`. Capacity comes from `Game.Capacity()`.
- [Game formatting](../internal/gameformat/formatter.go): shared announcement and keyboard generation for management and telegram.
- [Localization](../internal/i18n/i18n.go): `en`/`de`/`ru`, normalization, localized dates. The web UI is English-only. Group language/timezone and personal DM preferences have different resolution paths.
- [Management migrations](../migrations): embedded SQL applied during management startup.
- [Booking migrations](../cmd/booking/storage/migrations): independently versioned embedded SQL applied during booking startup using `booking_schema_migrations`. Both streams share the database/schema but do not share migration history ownership.

## Identity and trust

`users` is canonical; `user_identities` links provider/external IDs. Resolve Telegram identity through `POST /api/v1/identities/resolve`; `players` is created lazily on first participation. User-scoped actions use canonical `user_id`; game roster/result references may use `player_id`. Provider IDs remain legitimate at the Telegram boundary and for display, not as substitutes for canonical IDs in management calls.

Management enforces server-owner authority from `users.is_server_owner`. `SERVICE_ADMIN_IDS` seeds it at startup (grant-only), not a second live role source. Telegram-derived group administration is resolved using the Telegram API. Web derives identity/actor fields from its signed session and delegates owner decisions to management.

Internal bearer authentication assumes trusted owner-operated transports. Not every management route independently verifies end-user group authorization; bearer access is therefore powerful. Production Compose does not publish management/booking/PostgreSQL ports, but its bridge network is not an agent sandbox or a third-party plugin boundary. See [the operator trust model](../README.md#trust-model).

## Compatibility and releases

Each `cmd/<service>/VERSION` is injected at build time with `-ldflags "-X main.Version=<version>"`. CI/CD alone increments these files. Telegram checks management's `/version` at startup and exits on retrieval/major-version incompatibility; this is not universal contract negotiation across all services.

The [release workflow](../.github/workflows/release.yml) builds/releases selected services; promotion updates the stable image tag consumed by Watchtower. Promotion is production authority, not just metadata editing. Local agent tasks stop at a reviewable diff unless the owner explicitly requests further action.

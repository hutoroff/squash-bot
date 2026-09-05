# Service Discovery, Capability Registration, and Monitoring

**Status:** Proposed — not implemented.

**Code baseline reviewed:** `0bc2df1`.

**Scope:** Self-registration and runtime capability availability in Docker Compose, with Prometheus and an existing external Grafana instance.

## 1. Executive decision

Implement a small **registry module inside `management`**, backed by PostgreSQL. Do not introduce a separate discovery binary yet.

Services register their identity, instance-specific endpoints, versioned contracts, and capabilities. They renew a short lease while running. Consumers resolve a **logical service key**, rather than permanently binding to a particular instance URL. Business requests continue to use direct HTTP; the registry is not a proxy, message broker, or workflow engine.

Use Docker DNS for the registry's bootstrap address. Introduce a narrow resolver/registrar abstraction so infrastructure discovery can later move to Consul or an orchestrator without changing business services.

Add Prometheus to the Compose deployment. It discovers application scrape targets through the registry's HTTP SD endpoint. The existing external Grafana queries Prometheus and evaluates Grafana-managed alerts, including Telegram notifications independent of the application processes.

**Important boundary:** This design makes the discovery model replica-aware. It does **not** make existing `management`, `telegram`, or Eversports booking code safe to run as multiple active replicas. Their production replica counts remain one until separate concurrency work is completed.

## 2. Confirmed requirements and assumptions

### Confirmed with the owner

- Keep Docker Compose; no orchestrator migration now.
- Self-registration and runtime discovery of capabilities are more important than immediate horizontal scaling.
- One active `management` and one active `telegram` are sufficient.
- Booking replica safety will be designed separately.
- All deployed services are operated by the same trusted owner.
- If a dependency is unavailable, degrade and notify an administrator where possible. No offline command queue is required.
- Deployment interruptions and recovery measured in tens of seconds are acceptable.
- A small custom registry is acceptable if migration to an established solution remains practical.
- Prometheus monitoring is required; Grafana already runs on another instance.

### Deployment assumptions

- Containers can reach each other over one shared user-defined Docker bridge network.
- Services may be started separately, including from separate Compose projects attached to that network.
- PostgreSQL remains a required dependency of `management`, not a database shared directly with every service.
- External Grafana can be given a secure network path to Prometheus. The exact private-network or HTTPS-proxy setup is an implementation-time deployment choice, not a reason to expose application APIs publicly.

### Explicit non-goals

- New booking-system implementations or a complete provider-neutral booking API.
- Arbitrary plugins, remotely supplied executable code, dynamically generated UI, or importing unfamiliar APIs from manifests.
- Multi-active schedulers, distributed Telegram conversations, account-level booking coordination, exactly-once external operations, or automatic mutation replay.
- A service mesh, general-purpose event bus, configuration server, distributed lock service, or custom consensus protocol.
- Eliminating all bootstrap configuration or all required infrastructure dependencies.

## 3. Current architecture and relevant constraints

```text
telegram ──HTTP──▶ management ──SQL──▶ PostgreSQL
web ───────HTTP──▶ management ──HTTP─▶ booking ──HTTP──▶ Eversports

telegram and management independently call the external Telegram Bot API.
```

The code, rather than older comments/documentation, is the source of truth for the observations below.

| Area | Current behavior | Consequence |
|---|---|---|
| Wiring | `cmd/management/main.go` constructs a booking client only when `SPORTS_BOOKING_SERVICE_URL` is set. | A booking service appearing later cannot enable the integration without changing startup wiring. |
| HTTP clients | `cmd/telegram/client/client.go`, `cmd/web/webserver/*`, and `cmd/management/service/booking_client.go` retain static URLs. | Endpoint changes and compatibility selection are not represented explicitly. |
| Bootstrap | Telegram makes one management `/version` request and exits on failure or release-major mismatch. | Temporary dependency absence is treated as startup failure. |
| Compose | Production management depends on healthy booking; web/telegram depend on healthy management. | Startup ordering masks the lack of runtime dependency handling. |
| Scheduler | Every management process starts cron, audit retention, and startup changelog publication. | A second management instance duplicates potential executors. |
| Booking deduplication | `auto_booking.go` checks a result before booking and saves it afterward; `auto_booking_result_repo.go` has a unique slot key. | The DB constraint protects stored results, not external side effects from concurrent execution. |
| Notifications | `game_notifier.go` has process-local locks; telegram has its own edit coalescer. | Multiple processes can still publish stale or duplicate message updates. |
| Telegram transport | Wizard state is held in `sync.Map`; webhook acknowledges an in-memory enqueue; polling fallback deletes the webhook. | Multiple replicas and durable update processing require separate work. |
| Booking execution | `booking/handler.go` caches credential clients; `eversports/checkout.go` serializes checkout using a client-local mutex. | Account/session concurrency is not coordinated across replicas. |
| Routing scope | Eversports facility configuration is service-wide, not passed with every booking request. | Two booking processes with different facilities are not interchangeable replicas. |
| Failure handling | `booking_core.go` currently treats booking failures as credential failures and may continue using another account. | Infrastructure unavailability or an ambiguous timeout must not be mistaken for invalid credentials. |
| Booking readiness | `api/venues.go:bookingReadiness` checks settings and credentials, not live provider availability. | Existing readiness can report a usable integration when its service is absent. |
| Health | Current `/health` handlers return `ok`; telegram has HTTP health only in webhook mode. | Liveness is not dependency health, and polling telegram cannot currently be scraped. |
| Monitoring | `scripts/healthcheck.sh` checks selected containers; application code has no Prometheus instrumentation. | Monitoring needs actual instrumentation, not just dashboards. Indirect OTel dependencies in `go.mod` do not provide it. |
| Security | Internal HTTP APIs use one shared bearer secret on a private Docker network. | Acceptable for the current owner-operated deployment, but not a third-party plugin security boundary. |

## 4. Alternatives considered

| Option | Benefits | Limitations | Decision |
|---|---|---|---|
| Docker DNS and fixed logical URLs only | Almost no new infrastructure; sufficient for locating fixed services. | Does not provide an application capability catalog, lease metadata, or per-instance compatibility filtering. | Keep for bootstrap, not the entire solution. |
| Registry in management, PostgreSQL-backed | Smallest operational addition; uses existing persistence and ownership boundaries; survives restarts. | Registry availability is coupled to management/DB availability. | **Recommended now.** |
| Separate custom registry service | Independent lifecycle and failure domain. | Another deployable and storage/migration owner; most consumers still need management for useful work. | Extract later only if there is a concrete need. |
| Consul | Established registration, health checks, service metadata, DNS/HTTP lookup. | Additional operation/security/configuration work; still does not define booking contracts or fix replica safety. | Good future infrastructure backend, unnecessary now. |
| Kubernetes discovery | Native service/endpoints lifecycle under an orchestrator. | Requires a deployment-platform change the owner does not currently need. | Revisit if deployment changes independently. |
| NATS/RabbitMQ-based communication | Useful for durable commands and asynchronous work. | Changes invocation and failure semantics substantially; unnecessary for current synchronous APIs. | Not part of this proposal. |

A custom registry becomes a poor choice if it grows into replicated consensus, generic distributed locking, or a routing proxy. At that point adopt an established infrastructure component rather than extending it.

## 5. Target architecture

```text
                                Shared Docker network
                  ┌────────────────────────────────────────────┐
                  │ management                                 │
                  │  business API + singleton scheduler        │
                  │  registry API + capability catalog         │
                  │  registry storage ───────────▶ PostgreSQL  │
                  └───────────────▲────────────────────────────┘
                                  │ register / renew / resolve
                     ┌────────────┼─────────────┐
                  telegram       web         booking instances
                     │            │             ▲
                     └────HTTP────┴──▶ management ──HTTP────────┘

                  Prometheus ──HTTP SD──▶ registry
                       │
                       └──scrape──▶ each service's admin listener

          External Grafana ──secure query connection──▶ Prometheus
                 │
                 └──Telegram alert contact point──▶ Telegram Bot API
```

### Responsibilities

- **Registry:** instance membership, leases, immutable logical identity, contract declarations, and operator-readable status.
- **Capability catalog:** durable knowledge of which logical services were discovered, including services that currently have no live instances.
- **Resolver:** retrieves compatible live endpoints for a logical target.
- **Caller:** chooses a target, applies its execution policy, owns timeouts and safe retry semantics.
- **Management domain layer:** owns business enablement, credentials, venue configuration, and whether a capability is meaningful for a particular action.
- **Prometheus/Grafana:** metrics collection, visualization, and infrastructure alert delivery. Neither is required for application routing.

No business service should directly access registry SQL except through management's service/repository abstractions. The management-local resolver can query the registry service without calling its own HTTP API.

### Bootstrap is deliberately explicit

`DISCOVERY_URL=http://management:8080` is the initial directory address, resolved using Docker DNS. Registering management in its own catalog does not solve discovery of that initial address.

In registry mode, telegram/web use this URL for directory calls, then resolve the logical `management` service for business calls. Management registers itself through its local registry service once its business listener and required initialization are ready.

If management is unavailable, registration retries and affected business operations degrade. This is an accepted first-release failure domain. Later, a stable proxy/DNS address can front multiple management/registry instances, or registration can move to Consul.

## 6. Identity and capability model

### Three identities must remain separate

1. **Logical service key:** stable pool identity, e.g. `management`, `web`, `telegram`, or `booking.eversports.primary`.
2. **Implementation/provider:** e.g. `eversports`. This identifies the implementation, not a particular process or facility.
3. **Instance ID:** a random UUID generated once per process start. A restart creates a new ID; a hostname or shared Compose service name is not an instance ID.

A logical pool contains only replicas with the same routing scope. For current booking, the scope includes the configured Eversports facility identity and sport. Different facilities require different logical service keys even if both implementations are Eversports.

Do not use a password, credential ID, user ID, or venue name as a routing identity. Logical keys are bounded infrastructure identifiers.

### Registration descriptor — illustrative

```json
{
  "schema_version": 1,
  "instance_id": "<process-uuid>",
  "service_key": "booking.eversports.primary",
  "service_kind": "booking",
  "implementation": "eversports",
  "release_version": "<build-version>",
  "scope": {
    "facility_id": "<configured-id>",
    "facility_uuid": "<configured-uuid>",
    "facility_slug": "<configured-slug>",
    "sport": "squash"
  },
  "endpoints": {
    "api": "http://<instance-ip>:8081",
    "admin": "http://<instance-ip>:9090"
  },
  "contracts": [
    {"name": "eversports-booking", "major": 1, "minor": 0}
  ],
  "capabilities": [
    {"name": "booking.courts.list", "contract": "eversports-booking", "major": 1},
    {"name": "booking.slots.list", "contract": "eversports-booking", "major": 1},
    {"name": "booking.create", "contract": "eversports-booking", "major": 1},
    {"name": "booking.cancel", "contract": "eversports-booking", "major": 1}
  ],
  "state": "starting"
}
```

Names above become a small documented vocabulary during implementation, not arbitrary strings used to invoke arbitrary paths. Existing Eversports endpoints remain the wire API for this phase.

### Compatibility

- Release versions and API contract versions are different concepts.
- Initially describe the existing management API as `management-api` major 1 and the existing Eversports API as `eversports-booking` major 1. Confirm these declarations with contract tests before enabling registry mode.
- Resolve by contract name, supported major, required capability, and any required minor version. Minor additions must be backward-compatible within a declared major.
- An unfamiliar descriptor schema major is rejected. An unfamiliar API contract can be cataloged but is not executable by consumers that do not understand it.
- An implementation must not claim `booking.create` alone and thereby become interchangeable with every booking implementation. The associated contract is mandatory.
- During a rolling upgrade, compatible contract variations can coexist in a pool; a changed facility scope cannot.

### What “registers new functionality” means

A registration can make a **previously understood contract** available at runtime without restarting consumers. It cannot teach management how to speak an unfamiliar protocol.

This phase implements that mechanism for the existing Eversports integration. A future provider can be added without management code changes only after a provider-neutral contract and corresponding management adapter exist. Defining that complete contract, provider-specific settings, venue selection, and credential schemas is a separate design.

Registration does not automatically enable auto-booking for groups or venues, replace an existing provider binding, or grant a service access to credentials.

## 7. Storage and registry API

### Storage sketch

Add dedicated tables rather than placing registry JSON in `service_state`:

| Table | Main fields | Ownership/lifecycle |
|---|---|---|
| `service_catalog` | `service_key` PK, kind, implementation, canonical scope, last-observed contract/capability summary, first/last seen timestamps | Durable logical identity and diagnostic declarations; created on the first authenticated registration. Retained when instances disappear. |
| `service_instances` | `instance_id` PK, service key FK, endpoints, release, contracts/capabilities, reported state, terminal lifecycle state, last heartbeat, lease expiry | Ephemeral process memberships, persisted for restart recovery and bounded diagnostics. |

Use typed columns for selection fields and bounded JSONB for contracts/capabilities/scope. Index logical service key and lease expiry. Use PostgreSQL time for lease calculation and filtering; never trust caller wall clocks.

Register/upsert the catalog identity and instance atomically. For an existing key, reject kind/implementation/scope conflicts with `409`; do not silently overwrite the pool definition or mix different facilities. Descriptor identity, endpoints, and advertised contracts are immutable during a process incarnation. Changes require a new instance ID.

Do not delete live catalog references when their last instance expires. The catalog's last-observed declarations are historical diagnostics, never evidence that a capability is currently executable; routing always uses live instance declarations. A changed facility scope requires a new logical key and an explicit binding change, not overwriting the old catalog identity.

Deregistration retains a terminal tombstone rather than immediately deleting the row, so delayed registration/heartbeat requests cannot undo draining. Expired instances and terminal tombstones can be pruned after 24 hours, well beyond the bounded lifetime of control requests; the lifecycle client must never retry registration after shutdown begins. Expiry filtering must happen on reads; cleanup timing is not part of correctness. Never bind a business booking to an ephemeral instance ID.

### HTTP API sketch

All paths below are new and internal:

| Method/path | Behavior |
|---|---|
| `PUT /internal/discovery/v1/instances/{instance_id}` | Idempotent registration of a complete descriptor; response includes registry time and remaining lease duration. Identical retries are accepted; conflicting identity is rejected. |
| `POST /internal/discovery/v1/instances/{instance_id}/heartbeat` | Renew lease and update `starting`/`ready`/`not_ready` state. Return remaining lease duration. |
| `POST /internal/discovery/v1/instances/{instance_id}/drain` | Mark terminal draining state for this incarnation; exclude it immediately from routing. Later heartbeats must not revive it. |
| `DELETE /internal/discovery/v1/instances/{instance_id}` | Idempotent logical deregistration, retaining a terminal tombstone. A late request for an old UUID cannot remove a replacement UUID. |
| `GET /internal/discovery/v1/resolve?...` | Complete compatible, non-draining, non-expired endpoint list for a logical key/contract/capability. Empty list means a successful lookup with no eligible endpoint. |
| `GET /internal/discovery/v1/catalog` | Known logical services, declarations, instance status, and compatibility/availability reasons; no credentials. |
| `GET /internal/discovery/v1/prometheus-targets` | Prometheus HTTP SD JSON for application admin listeners; monitoring-only authorization. |

A heartbeat for an absent or expired membership returns `404`/`410` and requires a full registration. An expired process can register again using its descriptor, but a drained incarnation cannot be reactivated. Authentication is required on every operation; an instance UUID is not a credential.

A DB/registry failure is `503`, **not** a successful empty list. This distinction matters both to callers and Prometheus.

The first release needs a read-only catalog API, not an administrative registry UI. A Grafana dashboard supplies operational visibility.

## 8. Registration lifecycle and network addressing

### Proposed timing defaults

| Setting | Default |
|---|---|
| Heartbeat interval | 10 seconds with small jitter |
| Lease TTL | 35 seconds |
| Remote resolver refresh | 5 seconds for actively used logical targets |
| Registration/control request timeout | 3 seconds |
| Registration retry | Exponential backoff from 1 second to 10 seconds, with jitter |
| Prometheus HTTP SD refresh | 15 seconds |
| Prometheus scrape interval / timeout | 15 seconds / 5 seconds |
| Graceful shutdown budget | 10 seconds initially, aligned with existing HTTP shutdown |

These are operational defaults, not hard real-time guarantees. A crashed instance normally stops being selected within approximately TTL plus one resolver refresh. A successfully draining instance is excluded within the refresh interval. Metrics/alerts have their own delay.

### Startup

1. Validate local configuration and build a descriptor.
2. Start an internal admin HTTP listener, initially not ready, so startup/dependency problems are observable.
3. Bind the business listener where applicable and initialize required local resources.
4. Attempt registration asynchronously. Registry unavailability must not terminate otherwise healthy web/booking processes.
5. Report ready only when the advertised local capability can actually accept requests. Continue heartbeat/registration retry loops under the process context.
6. Consumers independently retry dependency resolution; they do not need a restart when a dependency appears.

Management still requires PostgreSQL and completed migrations to serve business/registry traffic. Independence from other **application containers** does not imply operation without the DB or the external Telegram/Eversports APIs. Existing external Telegram authorization during bootstrap needs bounded retries/clear health reporting; redesigning all external-client initialization is not a prerequisite for provider discovery.

### Shutdown

First report draining, then stop admitting new work, finish bounded in-flight work, and deregister best-effort. Keep the admin listener available during draining. A failed deregistration is handled by TTL expiry. Do not require a working registry to stop.

### Addresses in Compose

- Bootstrap uses a stable DNS name, e.g. `management:8080`.
- Instance advertisements use **instance-specific addresses**, not a shared service alias such as `booking:8081`. A shared alias cannot identify which replica a lease or Prometheus target describes.
- For the single-network Compose deployment, derive the process's container IP on the configured interface. Support `SERVICE_ADVERTISE_HOST` as an explicit override; fail registration with a clear error if auto-selection is ambiguous. Do not guess across multiple networks.
- IPs are lease-scoped observations, not persistent configuration. A replacement advertises its new address and UUID. Do not store these IPs in venues or credentials.
- Validate advertised addresses against the configured application network and approved ports. Do not advertise `localhost`, `0.0.0.0`, public ingress URLs, or arbitrary metadata-supplied metrics paths.
- Use a shared named external bridge network for independently deployed Compose projects. Create it once; all relevant containers, including Prometheus, join it. Prevent conflicting `management` aliases from unrelated deployments.
- No Docker socket access is required. Do not add it merely to obtain target addresses.
- The current network named `internal` is a bridge name, not a reason to enable Docker's `internal: true`; application containers still need internet egress.

## 9. Resolution, invocation, and failure semantics

### Interfaces and package boundaries

Introduce small infrastructure contracts in `internal/discovery`:

```go
// Sketch: concrete DTOs and error types are implementation work.
type Resolver interface {
    Resolve(ctx context.Context, selector Selector) ([]Endpoint, error)
}

type Registrar interface {
    Register(ctx context.Context, descriptor Descriptor) (Lease, error)
    Renew(ctx context.Context, instanceID string, state State) (Lease, error)
    Drain(ctx context.Context, instanceID string) error
    Deregister(ctx context.Context, instanceID string) error
}
```

Implement static and registry-backed resolvers. Keep the registry's business-facing implementation/interfaces in `cmd/management/service`, HTTP routes in `cmd/management/api`, and PostgreSQL repositories in `cmd/management/storage`. Infrastructure clients must not import management storage.

Inject endpoint resolution at the existing HTTP-client request boundary. Preserve domain interfaces such as `BookingServiceClient` where possible. Do not spread URL selection or discovery DTOs into scheduler job logic, Telegram handlers, or React components.

### Endpoint selection

- `Resolve` returns a list, not one permanent URL, even in the singleton deployment.
- Remote resolvers maintain bounded snapshots and refresh active targets periodically; management's local resolver may query the registry directly.
- Never use a cached endpoint past its advertised lease. Convert remaining lifetime to a local monotonic deadline conservatively, accounting for lookup duration. A successful empty snapshot replaces old candidates; a failed refresh can use only still-valid cached candidates.
- Reuse HTTP transports/connections, but stop selecting expired endpoints and retire their idle connections.
- A transport failure may temporarily exclude an endpoint locally. Do not globally evict services based on one caller's failed request. Business validation errors are not health failures.
- Target execution policy is caller/operator-owned, not something an instance can grant itself through metadata.
- Initially require a single eligible active instance for management and the current booking pool. Multiple eligible instances produce a visible `multiple_active_instances_not_supported` routing error, rather than arbitrary selection. Temporary overlap during replacement can therefore cause a short, acceptable interruption.
- The generic resolver can be tested with many interchangeable fake endpoints and later use round-robin selection. A routing guard is **not** scheduler leadership: running two management processes remains unsupported even if clients reject ambiguous routing.

### Retry and error policy

Use explicit error categories: `dependency_unavailable`, `contract_incompatible`, `multiple_active_instances_not_supported`, `credential_rejected`, and `operation_outcome_unknown`.

- Directory GETs, registration, and heartbeats can retry within a bounded budget.
- Side-effect-free business reads may retry once on another eligible endpoint when their contract permits it.
- Do **not** automatically replay booking creation, cancellation, guest addition/removal, publication, or other mutations following a timeout/disconnect/ambiguous server error.
- Resolve before dispatching a mutation. No endpoint means nothing was sent; return `503 dependency_unavailable` with a machine-readable reason.
- Once a booking request may have reached the provider, a transport failure does not prove that it failed. Surface `operation_outcome_unknown`, stop credential rotation for that attempt, and request administrator verification. A provider-side `5xx` does not by itself prove absence of side effects either.
- Only a positively identified credential rejection may mark a credential as failed. Do not reuse current string matching to classify discovery or network errors. Booking HTTP error mapping needs a small typed-error extension; ambiguous responses must be handled conservatively.
- Do not add retrying transport middleware or proxy retries for mutations. Preserve existing provider-specific retries only where their implementation has a proven safe boundary.

Discovery does not provide end-to-end idempotency. An operation ledger, request IDs/deduplication, provider reconciliation, and account coordination belong to the later booking reliability/scaling design.

### Degradation

- Web can continue serving its SPA. Management-dependent actions return an explicit temporary-unavailability response; existing sessions are not invalidated merely because management is down.
- Telegram stays alive with an admin listener while waiting for compatible management. Replace the one-shot startup version check with retrying contract discovery in registry mode. Do not start polling consumers until the initial dependency check succeeds; in webhook mode reject newly arriving updates with a retryable response while the dependency is known unavailable.
- A dependency can still fail after webhook admission. This release does not promise lossless processing: current in-memory acknowledgment and wizard limitations remain explicit.
- Management continues manual/non-booking functions when booking is absent. Booking readiness includes provider absence/incompatibility, not just stored credentials.
- Scheduled booking/cancellation attempts that are known not to have been dispatched are not recorded as successful operations. Existing narrow schedule windows remain; this phase does **not** add catch-up queues or replay missed deadlines. Notify admins of missed actions and require manual intervention when necessary.
- Never delete provider bindings, credentials, or booking history because a lease expires.

Infrastructure outage notifications come from Grafana. Existing application notifications remain responsible for specific failed booking/cancellation actions, with bounded/rate-limited repetition. No new notification service is required.

## 10. Runtime integration with the current booking service

In registry mode, always construct a discovery-backed booking client at startup. Its current absence is a runtime state, not a `nil` dependency that permanently disables the integration.

Use an explicit `BOOKING_SERVICE_KEY`, initially `booking.eversports.primary`, to select the existing integration. This replaces an instance URL with a stable logical binding. If unset, automated booking remains intentionally unconfigured. Preserve owner/group/venue enablement and credential checks.

The binding chooses one logical facility-scoped pool. Do not automatically choose the first service advertising `booking.create`, combine capabilities from unrelated pools, or fall back to a different facility when the configured pool is down.

For this phase, the service-wide binding is sufficient and avoids premature venue-schema changes. Introducing multiple selectable provider/facility integrations later requires durable per-venue integration bindings and provider-scoped booking identifiers. It must also preserve which provider created an existing booking so cancellation reaches the same provider. Those changes are deliberately deferred.

Extend the current booking-readiness response with stable reasons such as `booking_service_unavailable`, `booking_contract_incompatible`, and `booking_service_ambiguous`. Update Telegram messaging and the web readiness label map to recognize them. This is a small availability UX change, not a new provider-management UI.

## 11. Monitoring architecture

### 11.1 Service admin listener

Add an internal admin listener to **every** binary, including polling-mode telegram. Use `ADMIN_PORT=9090` inside each container; container-local port reuse is safe. It exposes:

- `GET /health/live`: process/admin loop is alive.
- `GET /health/ready`: readiness for the service's locally owned functionality.
- `GET /metrics`: Prometheus exposition, protected by a monitoring-only bearer token.
- `GET /version`: build/contract information for diagnostics.

Keep existing `/health` paths as liveness aliases for compatibility. Do not publish admin ports to the host or route them through the public web/webhook ingress.

Readiness must not recursively mean “all services everywhere are healthy.” Management requires its DB/schema, booking requires its advertised local configuration, and each listener must be initialized. Report downstream dependency availability separately. In particular, a missing optional booking service must not make management itself unready.

Use the official Go Prometheus client and its standard process/Go collectors. This is a justified new dependency. No OpenTelemetry collector, tracing pipeline, Redis, or exporter sidecar is required for the initial service-level monitoring.

### 11.2 Prometheus discovery

Use two application scrape jobs:

1. **Static management admin target** `management:9090`, so registry/management failure remains visible without successful discovery.
2. **HTTP SD targets** for the other registered application instances. Exclude management here to avoid duplicate scraping of the same instance.

Also scrape Prometheus itself statically. Static singleton targets are an intentional first-release bootstrap/monitoring exception; a future replicated management deployment will need per-instance targets plus an independently monitored bootstrap/registry endpoint.

The HTTP SD endpoint returns the standard JSON format, for example:

```json
[
  {
    "targets": ["<instance-ip>:9090"],
    "labels": {
      "service_key": "booking.eversports.primary",
      "service_kind": "booking",
      "instance_id": "<process-uuid>"
    }
  }
]
```

Monitoring target selection differs from request routing: include registered `starting`, `not_ready`, and `draining` instances while their leases are valid, not just routable ones. Exclude expired/deregistered entries. Do not add heartbeat timestamps or changing state as target labels, because changing labels creates new time series.

Return `200 []` only for a genuinely empty target set. Return `503` on registry/DB failure. Prometheus retains its current HTTP SD target list on refresh failure, but that cache does not survive a Prometheus restart [1].

Example configuration shape (to be implemented and checked with the selected Prometheus version):

```yaml
global:
  scrape_interval: 15s
  scrape_timeout: 5s

scrape_configs:
  - job_name: management
    static_configs:
      - targets: ['management:9090']
        labels:
          service_key: management
          service_kind: management
    authorization:
      type: Bearer
      credentials_file: /run/secrets/monitoring_token

  - job_name: discovered-services
    authorization:
      type: Bearer
      credentials_file: /run/secrets/monitoring_token
    http_sd_configs:
      - url: http://management:8080/internal/discovery/v1/prometheus-targets
        refresh_interval: 15s
        authorization:
          type: Bearer
          credentials_file: /run/secrets/monitoring_token

  - job_name: prometheus
    static_configs:
      - targets: ['localhost:9090']
```

HTTP SD authentication and metrics scraping authentication are separate configurations; both must be set. Mount secrets as files; do not expect Prometheus to substitute arbitrary environment variables inside YAML.

### 11.3 Minimum metrics

Metric names below are proposed contracts for instrumentation/dashboards:

| Metric | Purpose / labels |
|---|---|
| `squash_build_info` | Build and contract versions; an info gauge rather than version labels on every request counter. |
| `squash_service_ready` | Local readiness, 0 or 1. |
| `squash_http_requests_total` | Incoming requests by method, **route template**, status. |
| `squash_http_request_duration_seconds` | Incoming latency histogram with the same bounded routing dimensions. |
| `squash_dependency_requests_total` | Outgoing calls by logical target, operation, outcome category. |
| `squash_dependency_request_duration_seconds` | Outgoing duration by target/operation. |
| `squash_discovery_registration_success` | Whether this process currently has a successfully renewed valid lease. |
| `squash_discovery_last_success_timestamp_seconds` | Last successful registration/renewal. |
| `squash_registry_storage_healthy` | Whether registry storage can currently be read. |
| `squash_service_live_instances` | Management-exported count of eligible live instances for each monitored logical key, **including zero**. |
| `squash_service_expected_instances` | Operator-owned minimum live count for each monitored logical key. |
| `squash_scheduler_runs_total` | Completed scheduler runs by job and outcome, including skipped-dependency. |
| `squash_scheduler_last_completed_timestamp_seconds` | Detect a stopped/hung scheduler independently of HTTP health. |
| `squash_booking_operations_total` | Create/cancel outcomes: success, failure, unavailable, unknown; no account labels. |
| `squash_telegram_updates_total` | Processing outcomes; no chat/user labels. |
| Standard `go_*`, `process_*`, `up` | Runtime health and scrape success. |

Derive registry metrics from an authoritative bounded snapshot; do not return stale counts as current health when storage reads fail. Set the storage-health metric to 0 and omit unavailable membership counts until recovery. Do not make `/metrics` hang on an unbounded DB query.

Do not label metrics with user/chat/game/credential/booking IDs, email addresses, passwords, raw URLs, query strings, error messages, or arbitrary manifest metadata. Logical service keys and instance IDs are allowed, bounded infrastructure dimensions. Normalize unknown routes to one value rather than exporting arbitrary paths. Use request/operation IDs in logs, not metric labels.

### 11.4 Missing services must remain observable

`up == 0` alone is insufficient: once a dead instance is removed from HTTP SD, its `up` series eventually disappears rather than staying zero.

Configure an operator-owned `MONITORED_SERVICE_KEYS` inventory, initially `management,telegram,web`, plus `BOOKING_SERVICE_KEY` when booking is configured. Export expected/live counts for those keys even if a service has **never registered**. Instance registration must not be allowed to erase these expectations.

A successfully scraped management process must continue exporting `live_instances=0` for an expected service with no live eligible instance. Eligibility means ready/non-terminal/non-expired and, for an invoked dependency, matching its configured contract requirements. Count matching instances before applying the singleton ambiguity guard, so unsupported duplicates remain detectable. Each expected key has minimum count 1 in the first release; per-key replica counts can be introduced when scaling is enabled.

Unbound experimental services appear on dashboards but need not page the owner when stopped. Adding one to the required-service inventory is a deliberate operator action, separate from registration.

This protects alerts against both target disappearance and false “recovery” after expiration. If the registry itself is unavailable, static management scraping, registry-storage health, and Grafana data-source error handling provide the fallback signals.

### 11.5 Grafana and alerts

Deploy only Prometheus in the application Compose stack, with persistent TSDB storage and an initial 15-day retention plus a retention-size limit leaving free disk headroom. Pin a release/image digest during implementation. Size its memory/disk after measuring scrape cardinality; existing application container memory budgets are not a Prometheus capacity plan.

Configure a server-side Prometheus data source in the existing Grafana. Grafana cannot resolve the application's Docker-only `prometheus` hostname from another host and does not scrape applications itself.

Preferred connectivity: a private network/VPN address reachable by Grafana. If that is unavailable, publish only Prometheus's query interface behind an HTTPS reverse proxy with authentication and a source-IP allowlist. Bind the origin port to loopback or a private interface, and test Docker/firewall behavior from outside the host. Do not expose unauthenticated `:9090` to the internet [2]. Keep lifecycle/admin and remote-write receiver features disabled. Grafana's query credentials are separate from the internal scrape token.

Create Grafana-managed alert rules and a Telegram contact point on the external Grafana instance [3][4]. A separate monitoring bot token is preferable; delivery does not require application `telegram` or `management` to work. No Alertmanager is necessary for this first release. Prometheus-native alert rules are not automatically notification rules merely because Grafana can display them.

Initial dashboard sections:

- Service inventory, live/expected instances, local readiness, version, and last registration.
- HTTP request rates, error counts, and latency percentiles.
- Dependency failures and booking outcomes, especially unknown outcomes.
- Scheduler last completion and failure counts.
- Go/process memory, goroutines, and Prometheus storage/scrape health.

Initial alerts:

| Alert | Condition | Initial pending period |
|---|---|---|
| Management unreachable | Static management `up == 0`, or static series absent. | 1 minute |
| Required service missing | Live instance count below configured expectation. | 1 minute after discovery observes it |
| Registry storage unavailable | Registry storage-health gauge is 0. | 1 minute |
| Unsupported duplicate instances | More than one live management/telegram/current booking instance. | 30 seconds |
| Service not ready | Scrape succeeds but local readiness stays 0. | 2 minutes |
| Scheduler stalled | Last completed poll older than two configured poll intervals plus a margin. | 1 minute |
| Booking outcome unknown | Counter increase for an unknown external mutation outcome. | Next evaluation, without a long pending period |
| Monitoring unavailable | Grafana cannot query Prometheus / expected monitoring series disappear. | Approximately 1 minute, subject to Grafana version/settings |

Example service-missing expression:

```promql
squash_service_live_instances
  < on (service_key) squash_service_expected_instances
```

Example static management absence expression:

```promql
(up{job="management"} == 0) or absent(up{job="management"})
```

Configure Grafana No Data/Error handling explicitly for foundational health rules; missing data must not silently mean healthy [5]. Attach a fixed deployment label to the Grafana rules (or through scrape relabeling), and group alerts by deployment and logical service, not by every short-lived instance. Use a longer repeat interval (e.g. one hour), recovery notifications, and maintenance mute periods to avoid deployment noise. For low-traffic services, use failure counts rather than only percentages.

Keep `scripts/healthcheck.sh` as a temporary fallback during rollout, then disable overlapping cron alerts after end-to-end notification tests. If Grafana itself is down, this minimal design cannot deliver its alerts; independent monitoring of Grafana is outside this repository's scope.

## 12. Security boundaries

For this owner-operated first release, reuse `INTERNAL_API_SECRET` for authenticated registry operations and existing service invocations. This deliberately preserves the current coarse trust model; it does not isolate one registered service from another. Do not advertise it as a third-party extension sandbox.

Introduce a separate `MONITORING_TOKEN` used only for `/metrics` and `/internal/discovery/v1/prometheus-targets`. That token must not authorize registration, catalog mutation, identity resolution, credential APIs, or business actions. Refactor the existing all-or-nothing bearer middleware into explicit route groups so adding monitoring exemptions cannot accidentally expose application APIs.

Additional requirements:

- No registry/admin/metrics routes through the public web or Telegram ingress.
- Bound request size, instance count, descriptor fields, string lengths, and capabilities per descriptor.
- Validate advertised IP/network, allowed ports, and endpoint scheme. Construct request paths from known contract adapters. Ignore arbitrary metadata as routing instructions.
- Disable redirects on internal discovery/invocation clients so credentials are not forwarded to an unexpected destination.
- Do not add registry-driven arbitrary health probe URLs. Registration is a lease/self-report, not proof of reachability; real calls and Prometheus scrapes provide additional signals.
- Never put account credentials or bearer tokens in descriptors, discovery responses, logs, metrics, or Grafana dashboard definitions.
- Audit logical identity registration/conflict and operator policy changes where useful; heartbeat traffic belongs in metrics/debug logs, not a high-volume business audit trail.

If third-party services are introduced, per-service identities/scoped credentials, registration authorization, network isolation, and credential delegation become prerequisites. Do not simply share the existing internal secret more widely.

## 13. Configuration and compatibility rollout

Proposed configuration additions (exact naming can be finalized during implementation):

| Variable | Consumers | Purpose |
|---|---|---|
| `DISCOVERY_MODE=static\|registry` | Application services | Explicit migration/rollback mode; default static until cutover. |
| `DISCOVERY_URL` | Registry clients | Stable bootstrap URL, supplied by Compose. |
| `SERVICE_KEY` | Each service | Stable logical pool identity. |
| `SERVICE_ADVERTISE_HOST` | Each service | Optional explicit instance address; otherwise detect on the configured Docker interface. |
| `DISCOVERY_NETWORK_INTERFACE` | Each service | Interface to use for container address detection when needed. |
| `DISCOVERY_ALLOWED_CIDRS` | Management | Allowed advertised Docker network ranges; validate admin/API ports separately. |
| `ADMIN_PORT` | Each service | Internal health/metrics listener, default 9090. |
| `MONITORING_TOKEN` | Services; mounted file for Prometheus | Read-only monitoring authentication. |
| `BOOKING_SERVICE_KEY` | Management | Existing booking integration's logical target in registry mode; empty means unconfigured. |
| `MONITORED_SERVICE_KEYS` | Management | Expected services independent of discovery observations. |

Initially use fixed heartbeat/TTL defaults rather than exposing many timing knobs. Add configuration only when operational experience requires it.

- In **static mode**, retain `MANAGEMENT_SERVICE_URL` and `SPORTS_BOOKING_SERVICE_URL` semantics. Registration/monitoring may run in shadow mode without affecting routing.
- In **registry mode**, use logical bindings. A legacy URL is not an automatic failover path; unavailable discovery must not silently bypass scope/compatibility checks.
- Reject or explicitly warn about contradictory configurations. Document precedence, rather than guessing from which URL happens to be non-empty.
- Replace release-major equality with declared contract compatibility only for upgraded registry-mode clients. Retain existing version checks during the legacy-mode transition.
- Remove application-to-application `depends_on: service_healthy` as a correctness requirement after dependency handling is tested. Management may keep the DB startup convenience, but still handles DB failures explicitly.
- Keep production singleton replicas and stop-before-start deployments for management/booking/telegram. Remove fixed published ports or introduce ingress balancing **before** claiming web replica deployment support.
- Deploy new registry tables additively. Use the existing embedded migration mechanism for the singleton deployment; do not drop the tables during application rollback. Migration serialization/rolling schema compatibility are later management-scaling work.

## 14. Implementation work packages

The deliverable of this task is this proposal. The following packages are intended to become implementation issues, not changes already made.

| Package | Main changes | Depends on | Acceptance gate |
|---|---|---|---|
| **A. Contracts and health foundation** | Descriptor/error types, resolver/registrar interfaces, static resolver, internal admin listeners, monitoring auth, fixed metric vocabulary. | — | All four services, including polling telegram, expose protected metrics without exposing public admin endpoints. |
| **B. Registry and lifecycle** | Registry service/repository/API, additive migrations, self-registration, heartbeats/draining, catalog, scope validation, registration retry. | A | Services started before management register without restart; restart/expiry/conflicting-descriptor tests pass. |
| **C. Discovery-aware invocation** | Registry resolver in existing clients, explicit logical booking binding, runtime availability, contract filtering, typed errors, no ambiguous mutation replay, readiness UX updates. | B | Booking appears/disappears without management restart; absence does not cool down credentials or redirect to another facility. |
| **D. Prometheus and external Grafana** | HTTP SD endpoint, expected-service metrics, Compose Prometheus/storage, dashboard and alert provisioning artifacts, secure Grafana connectivity runbook. | A, B | New instances are scraped automatically; disappearance and whole-host/Prometheus failure alert through external Grafana. |
| **E. Cutover and operational documentation** | Shared Compose network, relaxed application startup dependencies, registry mode rollout, singleton guardrails, rollback runbook; README/AGENTS/CLAUDE updates. | C, D | Full failure/recovery drill succeeds; legacy rollback tested; no implied multi-active support. |

D can proceed alongside C. First deploy registration and monitoring in shadow/static mode; switch invocation only after observing stable leases and checking target scopes. Do not combine this rollout with management or booking horizontal scaling.

Expected code locations:

- `internal/discovery/`: DTOs, resolver/registrar clients, lifecycle helper, static implementation, tests.
- `internal/observability/`: bounded Prometheus instrumentation/admin listener helpers; no business rules.
- `cmd/management/service/`, `api/`, `storage/`: registry behavior, routes, persistence; discovery-aware booking integration and errors.
- `cmd/telegram/client/`, `cmd/web/webserver/`: endpoint injection and dependency-unavailability mapping.
- All `cmd/*/main.go` and `internal/config`: lifecycle/config wiring.
- `migrations/`: registry schema; update integration-test cleanup lists.
- `monitoring/`: Prometheus config, Grafana dashboard/alert templates, no committed secrets. Grafana artifacts are deployed to the existing external instance, not a new local Grafana container.
- `docker-compose*.yml`, `.env.example`, operational scripts/docs: deployment integration.

## 15. Test and acceptance plan

### Registry correctness

- Concurrent registrations of multiple UUIDs under one compatible logical pool preserve every membership.
- A conflicting facility/kind/implementation cannot overwrite a catalog entry.
- Register retries are idempotent; heartbeat updates use server time.
- Expired, draining, incompatible, and missing instances are excluded correctly. Cleanup delay never extends eligibility.
- A late deregistration/heartbeat cannot delete or revive a replacement process or revive a drained incarnation.
- Registry restart reloads state without extending existing leases. DB outage returns unavailable, not empty success.
- Unknown contracts remain visible but are not invoked.

### Communication and business safety

- Start booking/web/telegram before management; bring management up later. No application restart is needed to register/recover the internal dependency.
- Start management without booking; manual functions work; booking readiness is unavailable. Start booking and observe availability within the registration/refresh budget.
- Stop/crash/recreate booking with a different IP. Stale membership expires and the new instance becomes eligible without editing a URL.
- Exercise a two-instance fake stateless service to verify the resolver is genuinely set-valued; do not use real concurrent Eversports bookings as the discovery test.
- An incompatible instance cannot receive a business request. A different facility cannot receive a fallback request or its credentials.
- No-eligible-endpoint errors do not mark credentials invalid. A lost booking response does not trigger another account/instance attempt and is surfaced as unknown.
- Missing booking does not make management liveness fail or mutate a venue's enablement settings.
- Existing static-mode tests and behavior continue to pass; callback formats and in-place announcement editing remain unchanged.

### Monitoring and security

- Prometheus scrapes all four binaries in both Telegram transport modes.
- An instance not ready for business is still observable through its admin listener.
- New registrations appear in `/targets`; management is not scraped twice.
- An expected service that never registers exports a zero live count and alerts.
- Expiring the last instance removes its scrape target **without resolving** the required-service outage alert.
- Killing management triggers the static-target alert; killing Prometheus or blocking its external path triggers external Grafana Error/No Data handling.
- Scrape/SD credentials cannot invoke business or registration routes; no secrets appear in metrics/catalog output.
- Public ingress and the public origin interface cannot reach admin/registry endpoints or unauthenticated Prometheus.
- Use `promtool check config`; test recording/PromQL rules with fixtures where applicable. Import/provision Grafana-managed rules against the actual installed Grafana version and send a real test notification.

Run targeted Go unit tests first, PostgreSQL integration tests for lease/storage semantics, and then the broader existing suites. Use fake HTTP providers/fake clocks for failures and lease timing; do not make paid external bookings in CI.

## 16. Future migration and scaling gates

### Migrating infrastructure discovery

The stable application concepts are logical target, instance identity, contract, capability, and routing scope. SQL rows, HTTP registry paths, and heartbeat mechanics are replaceable infrastructure details.

For Consul, map logical keys to service names, instance UUIDs to service IDs, approved descriptors to metadata, and heartbeat/readiness to health checks. Resolve passing compatible entries through a Consul-backed resolver; Consul supports service registration and TTL checks [6]. For an orchestrator, use its service/endpoint discovery and metadata with an equivalent adapter.

Keep the **business capability catalog and integration policy in management**. Consul/Kubernetes can replace live endpoint membership but cannot replace the application's contract semantics or provider bindings.

Migration sequence: shadow-register into the new backend, compare resolved endpoint sets, switch one consumer class, then retire old lease writes. Avoid unconditionally merging two authoritative endpoint lists during cutover. Prometheus can move from HTTP SD to Consul/Kubernetes SD while preserving dashboard label contracts and expected-service monitoring.

This is a practical migration seam, not a promise of a zero-work or configuration-only migration. Health, authorization, and registration lifecycle semantics must be tested against the replacement.

### Gates before increasing replica counts

| Service | Required separate work |
|---|---|
| Management | Scheduler ownership/atomic task claims; concurrency-safe publication and notifications; booking operation coordination; bounded job contexts/shutdown; startup changelog deduplication; DB pool budget (currently 10 connections per process); safe schema migrations/rolling contracts. |
| Telegram | Explicit transport policy; no competing pollers or replica-triggered webhook deletion; shared/persisted conversations or partitioned processing; update deduplication/admission semantics; ordered message updates. |
| Booking | Account/session coordination across instances; checkout serialization; request/operation idempotency and ambiguous-outcome reconciliation; credential-cache concurrency; aggregate provider limits. |
| Web | Shared JWT/configuration, a health-aware public ingress without fixed host-port collisions, and validated dependency error handling. |

Do not use discovery leases as business-operation locks. Lease loss does not cancel a checkout already sent to an external provider, and external systems do not necessarily enforce fencing tokens.

## 17. Risks and accepted trade-offs

- **Management/DB remain a central failure domain.** Accepted now; external Grafana detects it, but discovery cannot keep DB-backed workflows operating without management.
- **A lease is not a reachability guarantee.** A fresh heartbeat can coexist with a failed business path. Real request errors and independent scraping remain necessary.
- **Self-described capabilities are trusted claims.** Contract tests and owner-operated deployment are the enforcement boundary in this phase.
- **Recovery is not catch-up.** Restoring a provider does not replay a missed midnight booking or cancellation deadline. Alerts must distinguish restored infrastructure from completed business work.
- **Ambiguous external outcomes require manual verification.** Prefer a visible unresolved operation over an automatic duplicate reservation or charge.
- **A logical binding is still configuration.** Discovery removes per-instance address management; it does not decide which facility/provider a venue should use.
- **Registry implementation must remain small.** If independent HA discovery or heterogeneous deployment becomes necessary, migrate the backend instead of implementing consensus.
- **Monitoring has its own availability limits.** External Grafana can alert when the application host/Prometheus disappears, but its own outage requires monitoring outside this stack.

No further product decision is required to begin planning packages A–E. Before deploying D, confirm the installed Grafana version, its alerting/provisioning access, the secure Grafana-to-Prometheus route, and the monitoring Telegram contact. These are deployment inputs rather than changes to the proposed architecture.

## References

1. [Prometheus HTTP service discovery: format, authentication, refresh/cache behavior](https://prometheus.io/docs/prometheus/latest/http_sd/).
2. [Prometheus security model](https://prometheus.io/docs/operating/security/).
3. [Grafana Prometheus data-source configuration](https://grafana.com/docs/grafana/latest/datasources/prometheus/configure/).
4. [Grafana Telegram alert contact points](https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/configure-telegram/).
5. [Grafana alert No Data and Error states](https://grafana.com/docs/grafana/latest/alerting/fundamentals/alert-rule-evaluation/nodata-and-error-states/).
6. [Consul service health checks, including TTL checks](https://developer.hashicorp.com/consul/docs/register/health-check/vm).
7. [Docker Compose networking, container replacement, and shared external networks](https://docs.docker.com/compose/how-tos/networking/).
8. [Prometheus configuration reference](https://prometheus.io/docs/prometheus/latest/configuration/configuration/).

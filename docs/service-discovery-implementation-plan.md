# Service discovery completion plan

**Status:** Planned; no implementation or deployment is performed by this document.

**Reviewed baseline:** `fffee2d`. The working tree was clean before this planning task. This plan covers completion of the [discovery design](service-discovery-design.md); it does not replace its safety requirements or the [booking cutover runbook](booking-reliability-runbook.md).

## 1. What is already implemented

| Package | Evidence at the reviewed revision | Assessment |
|---|---|---|
| A — contracts/admin foundation | `e94cc30`; [discovery contracts](../internal/discovery/discovery.go), [shared admin server](../internal/observability/admin.go), [booking admin server](../cmd/booking/booking/admin.go) | Committed. All binaries have protected admin endpoints; booking uses port **9091**, the others 9090. This is not yet the complete monitoring package. |
| B — registry/lifecycle | `8f613e1`; migration 040, [repository](../cmd/management/storage/service_registry_repo.go), [service](../cmd/management/service/service_registry.go), [API](../cmd/management/api/service_registry.go), [lifecycle](../internal/discovery/lifecycle.go) | Committed. Catalog, immutable memberships, leases, validation, registration, heartbeat and terminal states exist. Process lifecycle ordering still needs completion. |
| C — invocation | `fffee2d`; [remote resolver](../internal/discovery/resolver.go), [booking client](../cmd/management/service/booking_client.go), [Telegram client](../cmd/telegram/client/client.go), [web target](../cmd/web/webserver/mgmt_proxy.go) | Core committed, **not all acceptance requirements complete**. Logical routing, contract/scope filtering, singleton guards, lease-bounded caching, no static fallback and no explicit transport replay exist. |
| M — reliable booking caller | Earlier commits through `ea9585f`; management intents, typed v2 execution/recovery, cancellation/origin persistence, notifications and durable aggregate queries | Substantial implementation exists. Duplicate-risk allocation is an unexposed primitive, not an enabled caller workflow. Combined acceptance and operational cutover remain gates. |
| D — monitoring deployment | Metric-name constants and admin foundations only, plus existing booking instrumentation | No `monitoring/` artifacts, Prometheus Compose service, HTTP SD route, or expected-service collector. |
| E — cutover | Application-to-application health dependencies are already removed from current Compose files; DB dependencies remain | Shared external network, monitoring deployment, documented shadow/cutover/rollback and complete drill evidence remain. |

### Concrete remaining gaps

1. **Startup/shutdown semantics:** management starts its admin server after DB/migrations, Telegram authorization and startup work. Telegram also authorizes externally before exposing admin health. Web readiness is hard-coded true; Telegram marks transport ready before transport startup finishes. Shared shutdown drains and deregisters together, through deferred cleanup after listener shutdown. Management self-registers through HTTP instead of its local registry service. See the four `cmd/*/main.go` files and `internal/discovery/bootstrap.go`.
2. **Resolver completion:** remote refresh is request-triggered rather than periodic for active targets; endpoint removal does not explicitly retire idle connections. Existing tests cover selected lease/error cases, not the complete replacement/concurrency/failure matrix.
3. **Availability UX:** [booking readiness](../cmd/management/api/venues.go) checks configuration and credentials, not live dependency eligibility. The three proposed service-availability reasons are absent from Telegram localization and [frontend labels](../web/frontend/src/settingsLabels.ts). Some web paths return generic 502/503 instead of stable typed pre-dispatch discovery reasons.
4. **Telegram degradation:** registry-mode startup waits for management, but [webhook admission](../cmd/telegram/telegram/webhook.go) does not check subsequent known dependency unavailability. Initial startup waiting is not runtime outage handling.
5. **Monitoring backend:** no `/internal/discovery/v1/prometheus-targets`; no monitoring-only route group on management's business listener; no `MONITORED_SERVICE_KEYS`, expected/live counts, registry-storage health, or registration lease collectors.
6. **Instrumentation:** most names in [metrics.go](../internal/observability/metrics.go) are constants only. Shared incoming/outgoing HTTP, scheduler and Telegram update instrumentation is missing. Booking already has bounded `booking_*` metrics; management's reliable-booking aggregate endpoint is JSON, not a Prometheus collector.
7. **Deployment/operations:** Compose still creates a project-local bridge. No pinned Prometheus, TSDB configuration, Grafana artifacts, secure query-path runbook or independent alert-delivery evidence exists.
8. **Documentation drift:** the design overstates C completion; architecture/index/root instructions and parts of the booking runbook still describe discovery as absent. Existing app-to-app `depends_on` removal should not be planned as new work.

These are code-inspection findings, not a claim that the whole implementation has been audited. The final acceptance matrix must also cover registry capacity races, terminal/expired memberships, storage failure and unknown contracts rather than assuming the existing tests prove every design requirement.

## 2. Execution and commit rules

- Execute the numbered steps below in order by default. **Every step ends with its own local commit after its acceptance checks and diff review; do not accumulate several steps into one commit.** Suggested commit messages are provided.
- Keep each step buildable and testable. Add focused regressions in the same commit as their implementation; update the affected service reference in that commit, not only in the final documentation step.
- If a step proves too large, split it before implementation into named substeps, each with its own tests and commit. Do not mix unrelated cleanup into it.
- This request is a planning task: the steps and their commits have **not** been executed. No push, PR, release, image promotion, deployment, version bump or application changelog is included.
- Use fakes and disposable PostgreSQL. Do not start the ordinary application, read runtime secrets, contact real Telegram/Eversports, or change external Grafana during local implementation.
- Keep management, Telegram and booking single-active. Do not add scheduler leadership, durable update queues, provider-neutral plugins, generic mutation retries or multi-replica support.

## 3. Commit-by-commit implementation

### Step 1 — Finish process lifecycle and readiness (A/B follow-up)

**Scope:** `internal/discovery/{lifecycle,bootstrap}.go`, admin helpers and the four service entrypoints.

This is split into independently buildable commits to keep lifecycle changes reviewable:

1. **1a — lifecycle control and local ownership:** separate begin-drain/final deregistration, serialize and bound lifecycle control calls, prevent a late register/heartbeat from reviving a draining incarnation, use management's local registry service, and drain booking before its business/admin shutdown sequence. **Commit:** `fix(discovery): add two-phase lifecycle draining`.
2. **1b — observable startup/readiness:** start admin health before slow initialization, mark readiness only after required resources/listeners/transport initialize, and bound/expose external Telegram initialization. **Commit:** `fix(services): expose startup readiness before dependency initialization`.
3. **1c — shutdown sequencing coverage:** make management/web/Telegram signal shutdown invoke draining before business listener closure and add listener/registry-down/booking-executor ordering coverage. **Commit:** `test(discovery): cover service startup and draining order`.

- Registry failure must not prevent direct booking or SPA service, and optional booking absence must not fail management readiness.
- Preserve booking's tracked-executor shutdown budget and at least 110-second Compose grace. Define bounded handling for already-running management jobs without introducing scheduler leadership or catch-up.

**Step acceptance:** fake startup dependency failures remain observable; listener bind failure never reports ready; ordering tests observe draining while admin remains up; delayed register/heartbeat cannot revive shutdown; registry-down shutdown stays bounded; booking executor-drain regressions pass.

### Step 2 — Complete resolver refresh and endpoint retirement (C follow-up)

**Scope:** `internal/discovery/resolver.go`, internal HTTP client transport ownership and registry tests.

- Add cancellable bounded refresh for actively used selectors, with bounded cache retention and coalesced lookups; avoid serializing unrelated selectors behind a network request.
- Preserve conservative monotonic lease deadlines, successful-empty replacement and still-valid-cache-only behavior after refresh failure.
- Retire idle connections when endpoints leave a snapshot; never interrupt or replay an in-flight mutation to switch targets.
- Cover typed unavailable/incompatible/ambiguous results and exact binding requirements. Bound management-local registry operations as well as remote control calls.
- Extend registry correctness tests for restart/expiry, late terminal requests, unknown contracts, DB failure and concurrent capacity limits; fix any demonstrated boundary failures without growing a new registry framework.

**Acceptance:** fake-clock/race tests for refresh, shutdown and selector isolation; changed-IP replacement; expired endpoints receive no requests; no static fallback; failed mutations dispatch at most once; two fake eligible endpoints remain a set and invocation fails with the singleton ambiguity error.

**Commit:** `fix(discovery): bound active refresh and retire removed endpoints`

### Step 3 — Finish dependency availability responses and booking readiness (C follow-up)

**Scope:** management availability service/API, web proxy errors, Telegram messages, frontend readiness types/labels/tests.

- Add a non-mutating booking-availability service using the same configured contract, capabilities and exact scope as execution; do not send provider credentials just to determine registry availability.
- Preserve owner/group/venue/credential checks and return `booking_service_unavailable`, `booking_contract_incompatible`, or `booking_service_ambiguous` when appropriate.
- Map failures known to occur before dispatch to `503 dependency_unavailable` with a bounded machine-readable reason. Keep post-dispatch booking uncertainty distinct.
- Add en/de/ru Telegram explanations and English frontend labels. Preserve manual court editing, sessions, callback payloads and announcement keyboards.

**Acceptance:** absent → ready → expired/replaced transitions without management restart; incompatible/duplicate/wrong-facility targets never receive credentials or mutations; no credential cooldown on infrastructure failures; SPA and existing sessions survive outage; readiness/API/UI tests cover every new reason.

**Commit:** `feat(discovery): expose dependency availability in APIs and readiness UX`

### Step 4 — Complete Telegram runtime dependency gating (C follow-up)

**Scope:** Telegram bootstrap, dependency observation and webhook admission, using injectable local test seams.

- Keep registry-mode admin health alive while waiting for management; do not begin polling consumption before initial compatible dependency success.
- Track known management availability after startup. Reject newly arriving authenticated webhook updates with a retryable 503 while known unavailable or draining, without enqueueing/acknowledging them as accepted.
- Resume webhook admission after recovery without restarting the bot. Preserve existing static-mode compatibility behavior and do not claim lossless handling of already accepted in-memory updates.

**Acceptance:** startup with management absent, subsequent recovery/outage/recovery, unchanged static behavior, authenticated webhook rejection without queue insertion, and no unauthorized change to polling/webhook registration. Use fake Telegram endpoints only.

**Commit:** `fix(telegram): gate webhook admission on known dependency availability`

### Step 5 — Add Prometheus HTTP service discovery (D)

**Scope:** management repository/service/API and authentication routing.

- Add a bounded, PostgreSQL-time-based monitoring snapshot/query and the HTTP SD endpoint.
- Include unexpired starting, ready, not-ready and draining memberships; exclude deregistered/expired instances and management, which will be scraped statically.
- Emit validated admin host/port targets with bounded stable infrastructure labels; metrics path is fixed, not descriptor-controlled.
- Authenticate SD with the monitoring token only. Keep registry/business routes behind the internal API secret; do not add a broad authentication exemption.
- Return `200 []` only for genuine emptiness and `503` on storage failure.

**Acceptance:** disposable-DB tests for every membership state, no duplicate management target, booking port 9091, valid HTTP SD JSON, bounded timeout, and a token-permission matrix proving scrape credentials cannot register, resolve identities or mutate business data.

**Commit:** `feat(monitoring): expose authenticated Prometheus service discovery`

### Step 6 — Export authoritative inventory and registration health (D)

**Scope:** configuration, registry monitoring service/collector, lifecycle metrics and all admin registries.

- Implement bounded operator-owned `MONITORED_SERVICE_KEYS`, defaulting to management/telegram/web plus the configured booking binding. Registration cannot remove expectations.
- Export expected=1 and live counts including zero for never-registered keys. Apply readiness, lease, contract/capability and bound-scope eligibility; count before the singleton guard so duplicates remain visible.
- Use a bounded authoritative snapshot. On query failure export storage-health=0 and omit unavailable live counts, rather than returning stale counts as healthy. Preserve configured expectations.
- Wire registration-success and last-success metrics into all four processes; registration success expires with the lease and is not merely an “ever registered” flag.
- Complete build/contract metadata and any catalog availability diagnostics required by the design without using historical catalog declarations as live capability evidence.

**Acceptance:** never registered, last-instance expiry, duplicate instances, incompatible scope/contract, registry outage/recovery and expired registration lease. Scrape latency stays bounded and no business identifiers appear as labels.

**Commit:** `feat(monitoring): export expected services and authoritative lease health`

### Step 7 — Instrument HTTP and dependency calls (D)

**Scope:** shared observability, service routers and existing internal-client request boundaries.

- Wire request counters/histograms with method, route template and bounded status/outcome dimensions; normalize unknown routes and unsupported methods rather than labeling raw input.
- Instrument outgoing logical target/operation/outcome and duration in static and registry modes, including resolution failures before dispatch.
- Reuse booking's bounded instrumentation. Establish a documented common metric/query contract without counting the same provider execution twice or removing existing reliability signals.
- Keep bearer tokens, full URLs, queries, IDs, provider bodies and raw error messages out of labels.

**Acceptance:** gathered-metric tests for route normalization, pre-dispatch failures and actual dispatch; repeated arbitrary paths do not grow label cardinality; successful/failed requests are counted once; no request retry behavior changes.

**Commit:** `feat(observability): instrument bounded HTTP and dependency metrics`

### Step 8 — Instrument scheduler, Telegram and reliable-booking outcomes (D)

**Scope:** scheduler/job outcome seams, update processing, management booking collectors and existing booking metrics.

- Report bounded job outcomes including skipped dependency and last-completed timestamps. Current `scheduledJob.run` returns no result: introduce an explicit observation seam rather than counting every normal return as business success.
- Export the configured poll interval or otherwise parameterize the stall alert; a hung job must not advance its completion timestamp.
- Count Telegram processing outcomes without chat/user labels.
- Expose management's durable unknown/pending/risk aggregates and age through protected Prometheus collectors; reuse booking ledger/admission/unknown metrics. Same-key status/replay is not a new execution.

**Acceptance:** success/failure/skipped/hung-job fixtures, update failure fixtures, restart-safe unresolved gauges, same-key replay not incrementing execution counters, bounded DB collection and secret/identifier-free labels.

**Commit:** `feat(observability): expose scheduler and durable booking health`

### Step 9 — Add Prometheus and shared-network deployment configuration (D/E)

**Scope:** `monitoring/prometheus/` (new), Compose files, `.env.example`, synthetic configuration checks.

- Add a pinned Prometheus release/digest, persistent volume, 15-day and size-limited retention, resource limits and its own static scrape.
- Configure static `management:9090` plus authenticated HTTP SD and separately authenticated discovered scrapes. Use a secret file for Prometheus credentials, not YAML environment interpolation.
- Provide a named external bridge network for independently started production projects, with an explicit one-time creation procedure and matching allowed CIDR/interface configuration. Keep development networking convenient and isolated.
- Preserve already-removed app-to-app startup dependencies, DB startup convenience and booking stop grace. Wire expected-service configuration and explicit static/shadow/registry examples.
- Publish no application admin ports. Keep Prometheus loopback/private by default; do not enable lifecycle/admin or remote-write receiver features.

**Acceptance:** `promtool check config` with the selected image; Compose validation using synthetic variables and an explicit temporary env file, never the owner's `.env`; tests/checks for token file mounts, network membership, no public admin exposure and correct singleton/grace settings.

**Commit:** `feat(monitoring): add Prometheus and shared-network Compose configuration`

### Step 10 — Add external Grafana dashboards, alerts and secure connection guidance (D)

**Scope:** `monitoring/grafana/` (new), alert fixtures and monitoring operator documentation.

- Supply dashboard, server-side data-source and Grafana-managed alert templates compatible with the owner's installed Grafana version; do not deploy another Grafana or add Alertmanager.
- Cover inventory/readiness/version/leases, request latency/failures, dependencies, booking uncertainty, scheduler completion, Go/process and Prometheus health.
- Implement all design alerts, including never-registered/disappeared services, unsupported duplicates, missing static management, storage outage, scheduler stall and monitoring loss.
- Configure explicit No Data/Error behavior, deployment/service grouping, pending/repeat periods, recovery notifications and maintenance muting. Pair unknown-outcome counters with durable unresolved signals so restarts do not conceal existing uncertainty.
- Document private/VPN connectivity, or authenticated HTTPS proxy plus source allowlist and private origin binding. Grafana query credentials and its independent Telegram contact are separate from scrape/application credentials.

**Acceptance:** dashboard/alert schema and PromQL fixture checks, including target disappearance and zero-series cases. Actual Grafana provisioning, firewall verification and notification delivery remain step 14, not claimed by static validation.

**Commit:** `feat(monitoring): add external Grafana dashboards and alert templates`

### Step 11 — Finish the explicit duplicate-risk caller workflow (M, separate booking scope)

**Why separate:** discovery and monitoring can ship in shadow mode without enabling duplicate-risk creation. The unexposed allocator alone does not complete design §10.1 M4.

- Implement a minimal explicitly authorized operator/caller entrypoint; recommend manual per-operation authorization initially, not an automatic retry policy or new web UI.
- Establish management-owned authorization, not trust in a supplied actor field. Inspect/reconcile the original operation and check stopped-executor eligibility and scheduling windows before durable authorization.
- Persist one new child key, exact original scope/court/time, reason/provenance and explicit account choice. Keep the parent unknown; retain possible liability and separate notifications. Booking still atomically rechecks eligibility.
- Keep the policy disabled unless explicitly authorized, with no cancellation override. If a framework flag is introduced, default it disabled and update `docs/feature-toggles.md` and consistency tests in this commit.

**Acceptance:** unauthorized/default-disabled requests dispatch nothing; active original execution blocks replacement; concurrent duplicate authorization yields one child; parent/child evidence remains separate; deadline/scope/account safeguards and notification dedup hold.

**Commit:** `feat(booking): add explicit authorized duplicate-risk caller workflow`

**Scope gate:** obtain owner agreement on the concrete authorization surface before implementing this step. It may instead be explicitly deferred with risk replacement disabled, but then record M4 as deferred rather than calling the entire design complete. D/E infrastructure rollout does not depend on enabling it.

### Step 12 — Close combined discovery and reliable-booking acceptance gaps (C/D/E/M)

**Scope:** local HTTP/provider fakes, disposable-DB integration/lifecycle suites, monitoring fixtures and safe test harnesses.

- Build a traceable matrix from design §15 and §10.1 to actual tests; reuse existing tests and add only missing scenarios.
- Exercise dependency-first/consumer-first startup, management without booking, registration/recovery without restart, different-IP replacement, lease expiry, draining and registry/DB outage distinctions.
- Verify both Telegram transports, web session preservation, singleton ambiguity, wrong contract/scope rejection and no mutation replay/new-account fallback on uncertainty.
- Run the full M matrix in static v2 and registry modes: durable intent/key allocation, lost responses, crash/local-persistence recovery, partial/unknown results, cancellation semantics, origin/deletion guards, publication dedup, and direct booking with management stopped.
- Test verified legacy-import fixtures and mutation-disabled legacy rollback. Verify dynamic target addition/removal, expected-zero alerts, monitoring auth isolation and bounded metric labels using disposable monitoring infrastructure where needed.

**Acceptance:** focused tests, race tests, PostgreSQL integration and service/database lifecycle coverage, then `make check-fast` and `make check` on the final candidate. Run `make check-secrets` and `make check-security` separately; record known debt/tool/network failures honestly. No live provider mutations.

**Commit:** `test(discovery): cover combined failure recovery and monitoring acceptance`

### Step 13 — Finalize operator handoff and reconcile canonical documentation (E)

**Scope:** discovery/monitoring runbooks, root README/example config, architecture/invariants/service references, documentation index and affected design statuses.

- Document shadow registration and monitoring with static invocation first; compare scope/leases and observe stable collection before switching one caller class at a time.
- Document empty static URLs in registry mode, explicit logical booking binding, stop-before-start singleton replacement, network migration, secrets, retention and Grafana connectivity.
- Provide routing rollback to static **keyed v2**, preserving registry tables, both booking state owners, identity keys and unresolved evidence. Unsafe keyless rollback keeps mutations disabled.
- Keep booking's mutation pause, verified legacy inventory/import and whole-database backup/disposable restore as separate mandatory gates.
- Correct stale “not implemented” and “implemented locally” claims. Update `AGENTS.md` routing/status only where needed; do not copy project facts into `CLAUDE.md`.
- Include an operator checklist/evidence template, known in-memory Telegram and scheduler-window limitations, and criteria for disabling overlapping healthcheck cron alerts only after external notifications work.

**Acceptance:** local links/source paths, configuration examples, adapters and diff review; every acceptance requirement has evidence or an explicit unresolved gate. Repository-ready is not production-validated.

**Commit:** `docs(discovery): finalize shadow rollout cutover and rollback runbooks`

### Step 14 — Owner-authorized operational validation and cutover (E/booking gates)

**Not an automatic agent action.** Requires explicit access/deployment/notification authorization and the inputs below.

- Validate against the actual external Grafana version and secure network path; send a real test notification through the monitoring contact.
- Shadow-deploy, confirm all four binaries are scraped (including polling Telegram), and observe stable leases/expected counts.
- In an authorized drill, test last-instance disappearance, management loss, Prometheus/path/whole-host loss, recovery notifications and public-origin/firewall isolation.
- Complete the companion booking inventory/import and whole-database restore gates before enabling production mutations; preserve their mutation pause independently of discovery routing.
- Switch invocation only after the candidate checks and shadow/drill evidence pass; test static-v2 rollback and retain singleton deployment. Disable overlapping cron alerts only after notification verification.
- Record sanitized pass/fail evidence and residual limitations in the handoff document, not secrets or provider/account identifiers.

**Acceptance:** design D/E operational gates and applicable booking gates explicitly signed off by the owner. A failed drill blocks cutover; it does not become a documentation checkbox.

**Commit:** `docs(discovery): record authorized rollout and recovery drill results`

## 4. Dependencies and deployment inputs

Default order is 1 → 14. Steps 5–8 can be developed independently of the C UX work after agreeing lifecycle/collector seams; step 9 needs the SD/metrics contract, step 10 needs stable metrics, and steps 12–14 join the tracks. Step 11 is separate M scope and must not delay shadow monitoring, but its disposition must be explicit in final acceptance.

Confirm before finalizing step 10 or performing step 14:

- External Grafana version and provisioning/alerting access.
- Private/VPN query route, or the approved HTTPS proxy/auth/allowlist arrangement.
- Separate monitoring Telegram bot/contact and permission to send a real test notification.
- Production external-network name/subnet/interface, Prometheus disk budget/retention-size limit and deployment label.
- Owner's mutation-pause window, singleton replacement procedure and booking inventory/backup/import authority.

## 5. Evidence from this planning task

- **PASS:** source/history comparison against `fffee2d`; initial `git status --short` was clean.
- **PASS:** `go test -count=1 -timeout 120s ./internal/discovery ./internal/observability ./internal/config`.
- **PASS:** `git diff --check`; local Markdown link-path and skill-symlink validation; 14 ordered steps with 14 explicit commit boundaries; review of the new plan and tracked diff.
- **NOT RUN:** full/race/DB/frontend suites, Prometheus/Grafana validation, deployment and live operational drills. This task changes documentation only; focused passing unit tests do not certify A–C or the combined release.
- No application startup, production access, real booking/cancellation, external notification, commit or deployment was performed for this plan.

# Project documentation

Start with [AGENTS.md](../AGENTS.md) for agent working rules or [README.md](../README.md) for product/setup/operator instructions. This index is navigation, not a request to read every document.

## Current references

| Need | Reference |
|---|---|
| Service ownership, dependency boundaries, identity, compatibility | [Architecture](architecture.md) |
| Implemented feature flags, disabled defaults, scope, activation, rollback | [Feature toggles](feature-toggles.md) |
| Critical behavior and nearby tests/known gaps | [Invariants](invariants.md) |
| Current setup, commands, and verification limitations | [Development](development.md) |
| Local secret/dependency checks, scope, and remediation status | [Security checks and triage](security-checks.md) |
| Local task/review/handoff procedure and agent setup | [Agent workflow](agent-workflow.md) |
| Management business logic, authorization, persistence, results | [Management](services/management.md) |
| Scheduler windows, deduplication, booking and cancellation | [Management scheduling](services/management-scheduling.md) |
| Bot callbacks, wizard state, formatting, HTTP client | [Telegram](services/telegram.md) |
| Eversports session handling and checkout hazards | [Booking internals](services/booking.md) |
| Booking API/configuration and explicitly requested manual operations | [Booking operator reference](sports-booking-service.md) |
| Reliable booking backup, cutover, rollback, and incident handling | [Booking reliability runbook](booking-reliability-runbook.md) |
| Web session/proxy authorization, React and test conventions | [Web](services/web.md) |

Exact routes, signatures, configuration defaults, and database schema live in code and migrations. References explain decisions and hazards and link to those sources; they are not parallel API/schema catalogs.

## Plans — not descriptions of completed features

- [AI-first local development plan](ai-first-development-plan.md): staged improvements; its status/progress section identifies what exists. Steps 1–5 provide shared project knowledge, the local `make` verification interface, a focused regression pack, concise final-state handoff/review evidence, and lightweight local safeguards. Dependency scan results and the subsequent remediation are tracked separately; safeguards are not containment.
- [Service discovery, registration, and monitoring](service-discovery-design.md): **partially implemented**. A/B foundations and C core are committed; subsequent lifecycle, availability UX and monitoring work includes repository-local Prometheus configuration. Grafana and operational acceptance remain. Much of the §10.1 reliable management caller exists, but its explicit risk workflow and combined/cutover gates remain incomplete. Discovery does not provide multi-active replica safety.
- [Service discovery completion plan](service-discovery-implementation-plan.md): baseline `fffee2d`, verified remaining gaps and ordered implementation/acceptance steps, each ending in a local commit. Separates repository work from owner-authorized operational validation.
- [Booking reliability and discovery preparation](booking-reliability-design.md): **P1–P7 foundations and substantial management integration are committed; production mutation cutover remains blocked**. Booking-owned persistence, keyed v2 execution/recovery/import, lifecycle, observability and discovery registration are wired. Combined acceptance, legacy inventory/import and backup/restore/cutover drills remain gates; no exactly-once provider or multi-active guarantee.

## Documentation ownership

- Product behavior and operator configuration: relevant root README section (booking details in its operator reference).
- Internal behavior/rationale: the focused service reference; architecture and invariant map for cross-cutting changes.
- Feature flags: `feature-toggles.md` is the canonical inventory; update it in the same task as any flag/evaluation change. Structural checks do not replace semantic review.
- Development and agent procedures: development/workflow guides, with essential rules in `AGENTS.md`.
- Skills: short reusable procedures/routing in `.agents/skills/`. Claude directory links contain no independent knowledge.
- Plans: record scope, status, important decisions, and verification; retain historical baseline findings as historical, not current defects after they are fixed.

When code makes a reference inaccurate, update that reference in the same task. Do not duplicate a new route table, schema, or complete file tree just to make documentation appear comprehensive. A proposal becoming implemented needs an explicit status update and links to its implementation/tests.

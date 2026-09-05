# Project documentation

Start with [AGENTS.md](../AGENTS.md) for agent working rules or [README.md](../README.md) for product/setup/operator instructions. This index is navigation, not a request to read every document.

## Current references

| Need | Reference |
|---|---|
| Service ownership, dependency boundaries, identity, compatibility | [Architecture](architecture.md) |
| Critical behavior and nearby tests/known gaps | [Invariants](invariants.md) |
| Current setup, commands, and verification limitations | [Development](development.md) |
| Local task/review/handoff procedure and agent setup | [Agent workflow](agent-workflow.md) |
| Management business logic, authorization, persistence, results | [Management](services/management.md) |
| Scheduler windows, deduplication, booking and cancellation | [Management scheduling](services/management-scheduling.md) |
| Bot callbacks, wizard state, formatting, HTTP client | [Telegram](services/telegram.md) |
| Eversports session handling and checkout hazards | [Booking internals](services/booking.md) |
| Booking API/configuration and explicitly requested manual operations | [Booking operator reference](sports-booking-service.md) |
| Web session/proxy authorization, React and test conventions | [Web](services/web.md) |

Exact routes, signatures, configuration defaults, and database schema live in code and migrations. References explain decisions and hazards and link to those sources; they are not parallel API/schema catalogs.

## Plans — not descriptions of completed features

- [AI-first local development plan](ai-first-development-plan.md): staged improvements; its status/progress section identifies what exists. In particular, planned `make` commands are not available until Step 2.
- [Service discovery, registration, and monitoring](service-discovery-design.md): **proposed, not implemented**. Do not assume a registry, runtime capability resolution, metrics, or replica safety exists.

## Documentation ownership

- Product behavior and operator configuration: relevant root README section (booking details in its operator reference).
- Internal behavior/rationale: the focused service reference; architecture and invariant map for cross-cutting changes.
- Development and agent procedures: development/workflow guides, with essential rules in `AGENTS.md`.
- Skills: short reusable procedures/routing in `.agents/skills/`. Claude directory links contain no independent knowledge.
- Plans: record scope, status, important decisions, and verification; retain historical baseline findings as historical, not current defects after they are fixed.

When code makes a reference inaccurate, update that reference in the same task. Do not duplicate a new route table, schema, or complete file tree just to make documentation appear comprehensive. A proposal becoming implemented needs an explicit status update and links to its implementation/tests.

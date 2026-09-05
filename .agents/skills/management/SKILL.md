---
name: management
description: Use before changing management HTTP handlers, business rules, persistence, identity, results, or scheduler jobs in cmd/management. Routes to focused references and existing test seams.
---

# Management changes

1. Read [the management guide](../../../docs/services/management.md).
2. For cron, publication, booking, or cancellation, also read [scheduling and booking](../../../docs/services/management-scheduling.md).
3. Inspect current entry-point wiring, interfaces, relevant implementations, and nearby tests; do not assume perfect service/storage layering or copy a schema/signature from old prose.
4. Check [invariants](../../../docs/invariants.md) for identity, authorization, transaction, capacity, or external-side-effect changes.
5. Use [current verification commands](../../../docs/development.md). Database behavior needs real integration tests; Docker-unavailable skips are not passes.

Update affected canonical references, not this skill with exhaustive method/route inventories. Follow root AGENTS.md and the [local task procedure](../squash-task/SKILL.md).

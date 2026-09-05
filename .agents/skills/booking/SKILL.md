---
name: booking
description: Use before changing the Eversports client, session expiry, checkout/retry behavior, or booking HTTP handlers in cmd/booking. Also consult for cross-service booking failure semantics.
---

# Booking changes

1. Read [booking internals and checkout retry safety](../../../docs/services/booking.md).
2. Inspect the actual operation, handler interface, and local httptest fixtures. For management orchestration also read [scheduling and booking](../../../docs/services/management-scheduling.md).
3. Preserve per-request credentials, local-offset timestamps, and the mutation-specific retry boundary. There is no service-level account fallback and no general exactly-once guarantee.
4. Add tests for the relevant normal/error/HTML response shapes. Use the client's test baseURL seam, never a live account.
5. Run [focused verification](../../../docs/development.md). Operator API examples are not permission to book or cancel courts.

Update focused references, not a second API catalog. Follow root AGENTS.md and the [local task procedure](../squash-task/SKILL.md).

---
name: documentation
description: Use when updating project documentation or deciding which references a code change affects. Keeps shared knowledge canonical across Pi, Codex, and Claude Code.
---

# Documentation updates

1. Consult [the documentation index and ownership map](../../../docs/README.md).
2. Verify facts against current code/tests/migrations; distinguish intended behavior and unimplemented proposals from existing guarantees.
3. Update only affected references: product/setup in the root README, internal hazards in service docs, cross-cutting behavior in architecture/invariants, procedures in development/workflow docs.
4. Keep root AGENTS.md small and skills procedural. Do not duplicate full route/schema/signature inventories or put shared facts into the Claude adapter.
5. For every feature-flag addition, change, rename, removal, or evaluation change, update [the canonical inventory](../../../docs/feature-toggles.md) in the same task. Verify disabled defaults, scope, activation/failure/rollback behavior, and run registry/documentation consistency tests; prose accuracy still requires review.
6. Check local links, source paths, and skill adapter resolution after moves. Update an invariant's test status only when the check actually covers it.

Application changelogs require an explicit changelog request; documentation upkeep does not authorize them. See [the shared workflow](../../../docs/agent-workflow.md).

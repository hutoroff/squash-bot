---
name: squash-task
description: Use for a substantive squash_bot implementation or bug-fix task. Establishes scope, verifies assumptions, adds regression checks, and hands a local diff to the owner without publishing it.
---

# Bounded local implementation

1. Read root AGENTS.md; inspect working-tree status and preserve pre-existing work.
2. State the requested outcome and important constraints. Load only relevant service references via [the index](../../../docs/README.md), then inspect implementation and nearby tests.
3. Separate facts, proposals, and assumptions. Ask when uncertainty changes behavior, compatibility, security, or scope; investigate ordinary code questions yourself.
4. Define observable acceptance checks. Reproduce a bug before fixing it where practical; give a concrete reason if that is not feasible.
5. Implement the smallest coherent change. Preserve [critical invariants](../../../docs/invariants.md); do not add generic retries or expand authority to get unstuck.
6. Run focused checks while iterating, then applicable broader [current checks](../../../docs/development.md). Do not invent planned make targets or claim skipped checks passed. Re-run affected checks after the last edit.
7. Update affected documentation and inspect the final diff, including intended new files. Report blockers/unrelated failures rather than weakening checks.
8. Return [the concise local handoff](../../../docs/agent-workflow.md#local-handoff) with final-state evidence. Label each relevant check pass, fail, or not run, then stop for the owner's review.

No automatic commit, push, PR, merge, release, or deployment. Do not launch other model sessions just because a review workflow exists. Small edits need neither a design document nor a reviewer loop. These instructions do not isolate the agent from host credentials or files.

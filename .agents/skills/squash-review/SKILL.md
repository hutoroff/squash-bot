---
name: squash-review
description: Use for an independent local review of squash_bot changes before the owner commits or opens a PR. Focuses on concrete regressions, security, compatibility, failure paths, and missing tests.
---

# Findings-focused local review

1. Read root AGENTS.md. Establish the task, acceptance criteria, agreed constraints, base revision, and review scope from the [review brief](../../../docs/agent-workflow.md#fresh-review); ask if these cannot be determined.
2. Inspect the actual diff (staged, unstaged, and intended untracked files as applicable). Separate pre-existing owner work. A summary or diffstat is context, not evidence of the changed code.
3. Read relevant code, [service references](../../../docs/README.md), and [invariants/test gaps](../../../docs/invariants.md). Trace identity, authorization, partial failures, external retries, state transitions, and compatibility through changed boundaries.
4. Inspect regression tests and final-state verification evidence. Distinguish running a test from proving it exercises the risky path; use [current checks](../../../docs/development.md) only if verification is part of the review request. A failed or missing check remains failed or not run.
5. Report actionable findings first: severity, file/location, concrete triggering scenario, consequence, and suggested regression check. Separate confirmed defects from unresolved questions and verification limitations.
6. Omit speculative refactors and formatting comments covered by tooling. If no findings, say so along with verification limitations; do not claim exhaustive correctness.
7. Do not edit code, commit, publish, or launch more reviewers unless requested. One review pass is the default, not an autonomous retry loop.

Use [the local workflow](../../../docs/agent-workflow.md#fresh-review). Read-only review instructions and available tool selections are not a host security boundary; avoid secret files and live application operations.

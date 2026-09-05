---
name: squash-review
description: Use for an independent local review of squash_bot changes before the owner commits or opens a PR. Focuses on concrete regressions, security, compatibility, failure paths, and missing tests.
---

# Findings-focused local review

1. Read root AGENTS.md. Establish the task, acceptance criteria, agreed constraints, base revision, and review scope; ask if these cannot be determined.
2. Inspect the actual diff (staged, unstaged, and intended untracked files as applicable). Separate pre-existing owner work. The implementer's summary is not proof of correctness.
3. Read relevant code, [service references](../../../docs/README.md), and [invariants/test gaps](../../../docs/invariants.md). Trace identity, authorization, partial failures, external retries, state transitions, and compatibility through changed boundaries.
4. Inspect regression tests and claimed verification. Distinguish running a test from proving it exercises the risky path; use [current checks](../../../docs/development.md) if verification is part of the review request.
5. Report actionable findings first: severity, file/location, concrete triggering scenario, consequence, and suggested regression check. Separate confirmed defects from unresolved questions.
6. Omit speculative refactors and formatting comments covered by tooling. If no findings, say so along with verification limitations; do not claim exhaustive correctness.
7. Do not edit code, commit, publish, or launch more reviewers unless requested. One review pass is the default, not an autonomous retry loop.

Use [the local workflow](../../../docs/agent-workflow.md#fresh-review). Read-only review instructions and available tool selections are not a host security boundary; avoid secret files and live application operations.

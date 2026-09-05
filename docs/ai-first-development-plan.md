# AI-first development: local-first implementation plan

**Status:** Steps 1–5 and the completion-audit timeout follow-up are implemented locally (2026-09-05), ready for owner review. Dependency security findings remain open and outcome measurement continues over real tasks; the milestone does not claim a clean vulnerability baseline, measured productivity gains, or host containment. See [implementation progress](#11-implementation-progress) and [security triage](security-checks.md).

**Assessment baseline:** `f9622ab` (2026-09-05). Recheck the baseline before implementation.

**Scope:** A small, model-independent development workflow for Pi, Codex, and Claude Code. Agents implement bounded tasks locally; the owner runs/reviews local verification and decides when to create a PR. No application AI functionality is proposed.

## 1. Agreed decisions

| Topic | Decision |
|---|---|
| Supported tools | Preserve Pi, Codex, and Claude Code; choose models according to current performance. Do not hardcode model preferences into project workflows. |
| Autonomy | Unattended implementation of bounded tasks, followed by human review. |
| Execution | Keep agents on the existing macOS host. No new VM, container sandbox, or isolation changes in this initiative. |
| Collaboration | One developer today; avoid rules that require a second human to complete local work. |
| Delivery | Run checks and review code locally before creating a PR. Do not automate PR publication, pushing, merging, releases, or deployment. |
| Main problems | Incorrect assumptions, slow reviews, missing tests. |
| Outcomes | Fewer regressions, higher result quality, lower total agent/rework cost. |
| Credentials | Development and production credentials/accounts are separate, according to the owner. |
| Model data policy | No additional project-specific restrictions on sending information to hosted models. This is not a reason to read or disclose secrets unnecessarily. |
| GitHub | Free plan. Existing remote CI remains useful but is not the primary agent feedback loop. |
| Budget | A few focused days; prioritize a small usable baseline over completeness. |
| Other work | Complete this milestone before implementing service discovery. Do not implement discovery or monitoring here. |
| Documentation | Reorganizing `.claude/skills` and removing the automatic full-reference injection hook are acceptable. |

### Security boundary and accepted residual risk

The owner explicitly chose to keep current host execution and isolation unchanged. This is a limitation of the target state, not an unfinished requirement to introduce a sandbox later in this milestone.

The assessment verified that the local `gh` identity has repository ADMIN access and that the active main/dev ruleset includes a repository-role bypass. An agent able to use that shell may be able to act with the owner's GitHub authority. Markdown rules, skills, local scripts, secret scanning, and code review do not technically prevent that. A worktree also does not provide credential or host isolation.

Normal agent tasks must be instructed not to push, publish PRs, merge, release, promote images, use production resources, or initiate real bookings/cancellations without an explicit request. These are workflow restrictions, not enforced containment. Changes to host permissions, credentials, global agent settings, or GitHub settings require a separate explicit request.

The initiative improves development reliability and accidental-error detection. It must not be described as providing secure containment of an adversarial or compromised agent.

## 2. Target workflow

1. The owner supplies a task and any acceptance criteria or product constraints.
2. The agent reads the short root guide and the references relevant to the task.
3. The agent distinguishes observed facts, documented intentions, and assumptions. It asks about uncertainty that can change behavior, compatibility, security, or scope; it resolves ordinary code questions by inspection/tests.
4. For a bug, the agent reproduces it with a focused test where practical. For a feature, it defines observable acceptance checks before implementation.
5. The agent makes a bounded change, without unrelated refactoring or version/changelog changes.
6. The agent runs focused checks while iterating, then the full local verification command before handoff. A blocker must be reported as a blocker, never as a pass.
7. For substantive changes, a fresh review session checks the task, diff, and regression coverage. The owner may choose any supported tool/model. One review pass is the default; no automatic multi-agent loop.
8. The agent returns a concise review handoff and stops. The owner inspects the local diff and can rerun the same checks before deciding whether to commit or create a PR.

Committing is not automatic: commit only when explicitly requested. Do not reset, stash, overwrite, or clean unrelated owner changes. Check the initial working-tree state and distinguish pre-existing changes in the handoff.

### Definition of done for a task

- The requested behavior and important compatibility constraints are satisfied.
- Relevant regression/acceptance tests were added or a concrete reason for not adding them is given.
- Required local checks ran against the final code state; subsequent changes invalidate earlier evidence for affected checks.
- Documentation is updated only where behavior or working instructions changed.
- Remaining assumptions, missing verification, and risks are explicit.
- No push, PR, release, promotion, or deployment was performed as a side effect.

A small typo does not need a design document or model review. Use a short plan for multi-file or risky changes and a persistent execution plan only for work that needs decisions/progress to survive multiple sessions.

## 3. Verified baseline

The following was observed during the assessment, not guaranteed for later commits:

| Check | Result |
|---|---|
| `go test -count=1 -timeout 120s ./...` | Passed |
| `go test -race -count=1 -timeout 120s ./...` | Passed |
| Integration-tagged tests with real Testcontainers PostgreSQL | Passed |
| `go build ./...` in the existing checkout | Passed |
| `go vet ./...` | Passed |
| Frontend application TypeScript check | Passed; test files are excluded by the existing configuration |
| Frontend tests | 107 passed across 10 files |
| `go test -tags e2e -run '^$' -timeout 60s ./tests/e2e/...` | Failed to compile: outdated service constructors and participation calls |
| `gofmt -l` over tracked Go files | Four files reported |
| Clean export, compile embedded web package before building frontend | Failed because `web/frontend/dist` is absent |

Selected Go statement coverage with integration enabled: management service 68.1%, management API 25.6%, Telegram handlers 6.1%, web backend 72.4%. These are diagnostic measurements, not quality scores or proposed percentage gates.

### Concrete gaps driving the plan

- `AGENTS.md` contains stale Go requirements, removed Eversports credential configuration, and obsolete directory/job descriptions.
- Important restrictions, including CI-only version bumps, are explicit in `CLAUDE.md` but not the portable root guide.
- The management reference documents removed schema fields and contradicts current service/storage dependencies.
- `.claude/hooks/service-context.sh` injects entire matching service references. A representative management-plus-Telegram planning prompt injected about 103,000 characters / 1,271 lines.
- Integration `TestMain` implementations exit successfully without running tests when Docker is unavailable.
- CI does not select the stale E2E-tagged suite.
- The existing E2E test starts PostgreSQL and exercises service/storage code; it is not full transport/browser end-to-end verification.
- There is no single local bootstrap/check interface.
- `web/frontend/src/auditEvents.test.ts` already demonstrates an effective executable cross-language drift check and should be preserved.

### GitHub configuration observed

Read-only API inspection found a public repository with an active `main/dev` ruleset, required `build-and-test` and `frontend-test` checks, review requirements, and a role-based bypass. No GitHub Environments were returned. Rulesets, rather than legacy per-branch protection, provide the observed branch protections. Do not infer a lack of branch protection from the legacy branch-protection endpoint returning 404.

No GitHub configuration changes are included in this local-first milestone.

## 4. Knowledge and skill structure

Use a single canonical knowledge base with thin harness adapters:

```text
AGENTS.md                              essential rules, commands, reference map
CLAUDE.md                              @AGENTS.md import; tool-specific notes only

.agents/skills/
  squash-task/SKILL.md                  bounded implementation and handoff procedure
  squash-review/SKILL.md                independent, findings-focused review procedure
  management/SKILL.md                   short routing guide to relevant references
  telegram/SKILL.md
  booking/SKILL.md
  web/SKILL.md

.claude/skills/<name>                   relative links to shared skill directories

docs/
  README.md                            index: current, proposed, and historical documents
  architecture.md                      important boundaries and deliberate exceptions
  invariants.md                        critical behavior linked to code/tests and known gaps
  development.md                       bootstrap, verification, troubleshooting
  agent-workflow.md                     task/review/handoff and accepted security limits
  services/                            focused service references, split by topic as needed
  ai-first-development-plan.md          this plan; implementation status and decisions

Makefile
scripts/checks/                         small shared verification helpers where needed
```

This is a proposed destination, not a requirement to create empty files or split every paragraph. Keep reference content proportional to its value.

### Portability rules

- Pi and Codex discover root `AGENTS.md` and project `.agents/skills`. Pi project skills require project trust; document the one-time trust step without enabling global auto-trust.
- Claude Code imports `AGENTS.md` from `CLAUDE.md` and reads the shared skills through supported directory symlinks.
- Verify instruction and skill discovery in the actual installed tool versions. Startup discovery of nested instructions differs by harness; root instructions must explicitly route to any required deeper guide.
- Use common skill frontmatter and plain Markdown. Do not rely on Claude-only variable substitution, shell injection, model overrides, forked-agent fields, or tool grants for portable behavior.
- Avoid loading the same shared skill through multiple discovery paths.
- Keep model/provider selection and personal preferences out of checked-in project defaults.
- Preserve the existing changelog procedure if relocating it, but do not generate or modify application changelog entries.

### Content rules

- Keep the root guide roughly 100–150 concise lines, not a copy of every service reference. This is a design target, not a reason to remove critical restrictions.
- Keep skills short and procedural. Link to references rather than embedding complete API/schema inventories.
- Prefer authoritative code, schema, and tests for details that can be inspected. Document rationale, non-obvious hazards, and stable invariants.
- Every important invariant should point to relevant implementation and tests; mark coverage gaps explicitly.
- Do not turn a documented architectural aspiration into a false statement of current behavior. Record existing exceptions; avoid broad refactors solely to make a diagram true.
- Mark proposals as proposed. The discovery design remains separate and unimplemented.

## 5. Local implementation increments

These are independently reviewable local change sets, not instructions to publish PRs. Estimates are planning ranges, not guarantees. Stop at each boundary for local review if requested.

### Increment 1 — Correct and consolidate project knowledge

**Priority:** P0. **Budget:** approximately 0.5–0.75 day.

**Likely files:** `AGENTS.md`, `CLAUDE.md`, `.claude/settings.json`, `.claude/hooks/service-context.sh`, existing/shared skill directories, focused `docs/` references, affected README sections.

**Work:**

1. Consolidate essential rules and verification expectations in the portable root guide.
2. Correct the confirmed stale facts against code and migrations.
3. Remove the full-reference prompt hook and its registration; use normal on-demand discovery.
4. Move useful service knowledge to focused canonical references, pruning duplicated inventories.
5. Add shared task/review procedures and thin tool adapters.
6. Correct the documentation-update map so future agents do not rebuild parallel sources of truth.

**Acceptance:**

- All three tools can find the same essential rules and relevant skill.
- The representative cross-service prompt no longer causes automatic full-reference injection.
- Known stale facts from the assessment are resolved.
- No application code, service versions, or application changelogs change in this increment.

### Increment 2 — One reliable local verification interface

**Priority:** P0. **Budget:** approximately 0.75–1 day.

**Likely files:** `Makefile`, minimal `scripts/checks/` helpers, `internal/testutil/testdb.go`, integration `TestMain` files, `tests/e2e/environment_test.go`, frontend package configuration if needed, `docs/development.md`, `.github/workflows/ci.yml`.

**Command contract:**

| Command | Behavior |
|---|---|
| `make doctor` | Report Go/Node/npm/Docker prerequisites and missing generated assets without reading or printing `.env` contents. No application startup or environment mutation. |
| `make bootstrap` | Install locked frontend dependencies and build embedded assets. Do not overwrite host toolchains, global agent settings, or credential files. |
| `make check-fast` | Check formatting, diff whitespace, Go vet, application TypeScript, Go unit tests, and frontend tests. No live application services or Docker requirement after bootstrap. |
| `make check` | Rebuild relevant generated assets, build, run fast checks plus race-enabled Go verification, PostgreSQL integration tests, and the repaired service/database lifecycle test. Nonzero exit when any required check fails or cannot run. |
| `make check-security` | Run the selected pinned local security checks; separate from the fastest edit/test loop. |

Keep normal targeted `go test` and `npm test` commands available. Do not create a complex change-impact engine; shared-code changes make naive path-based test skipping risky.

**Work:**

1. Handle missing/stale embedded frontend assets explicitly. Bootstrap downloads may require internet; do not claim completely offline setup.
2. Make explicitly requested integration tests fail when Docker is unavailable. Share this behavior across the current `TestMain` implementations.
3. Repair the lifecycle test for current identity, constructor, and domain contracts. Prefer existing Testcontainers helpers to its fixed shared Compose port when practical.
4. Keep full browser/transport E2E outside scope; describe the repaired test accurately.
5. Make scripts portable across the current macOS environment and Linux CI, bounded by timeouts, and noninteractive.
6. Checks report status and actionable diagnostics; they do not silently format source files, delete user resources, or alter Git history. Generated ignored build/test artifacts are expected.
7. Reuse these scripts in existing CI where practical, keeping required check names and release-check expectations intact. No remote workflow execution or publication is required to finish local work.

**Acceptance:**

- A clean checkout has a working documented bootstrap/check sequence.
- A missing Docker daemon produces an explicit verification failure, not a success-shaped skip.
- The maintained lifecycle suite compiles, runs locally, and is selected by CI.
- Checks do not load the normal runtime `.env`, start production-style cron, send Telegram messages, or make real bookings.
- Failure output distinguishes missing prerequisites from code/test failures.

### Increment 3 — A small high-value regression pack

**Priority:** P0. **Budget:** approximately 0.75–1 day; scope capped.

**Likely files:** focused tests in `cmd/telegram/telegram`, a new `cmd/telegram/client/client_test.go`, and existing booking/service test files as warranted by the gap review.

**Selection:** First check existing tests. Add roughly three meaningful scenario groups rather than duplicating already-covered cases or pursuing a percentage target:

1. Telegram authorization/callback behavior: unauthorized mutation attempts, stable callback payloads, and the intended announcement/keyboard behavior.
2. Telegram HTTP client contracts: representative canonical user/actor propagation, request/response shapes, and error/status mapping.
3. Booking failure semantics: partial success, uncertain outcomes, and protection against unsafe duplicate retries.

Reuse `httptest`, existing fakes, and isolated database helpers. Prefer behavioral checks over implementation-specific assertions.

**Acceptance:**

- Each added scenario catches representative broken behavior; establish that the check would fail without the behavior it protects.
- Tests do not require external accounts or live Telegram/Eversports access.
- The invariant map links to the new checks and states remaining gaps honestly.
- If meaningful coverage requires a substantial production refactor, document that follow-up rather than expanding this increment silently.

### Increment 4 — Fast local review and evidence

**Priority:** P1. **Budget:** approximately 0.25–0.5 day; procedural content can land with Increment 1.

**Likely files:** `.agents/skills/squash-task/SKILL.md`, `.agents/skills/squash-review/SKILL.md`, `docs/agent-workflow.md`, small local report support only if useful.

**Work:**

- Define a short handoff containing the goal, behavior changed, important decisions, tests added, exact verification results, remaining risks, and relevant diff scope.
- For a fresh review, supply the task, agreed constraints, base revision, and actual diff. Do not rely on the implementer's summary as evidence of correctness.
- Review staged and unstaged changes as appropriate without mixing unrelated owner work into the review scope.
- Findings must identify a concrete failure mode and file location. Do not fill reports with formatting feedback or speculative refactoring.
- Keep model review optional for trivial changes and one pass by default for substantive changes. Deterministic checks run before spending on model review.
- Store temporary logs/reports under an ignored location such as `tmp/agent/` if needed. Do not commit a transcript or journal for every small task.
- A review prompt or read-only tool configuration is not a universal security boundary; do not claim otherwise.

**Acceptance:**

- The owner can review the code locally without reading the full implementation conversation.
- Verification evidence refers to the final code state and distinguishes pass/fail/not-run.
- No automatic model calls, paid review loop, Git commits, pushes, or PR creation are hidden in a check target.

### Increment 5 — Lightweight local safeguards

**Priority:** P1. **Budget:** approximately 0.25–0.5 day, subject to findings.

**Likely files:** security-check scripts/configuration, `Makefile`, `.gitignore`, affected workflow/development guidance.

**Work:**

- Add a pinned local secret scanner for the prospective change set. Support staged/unstaged/untracked candidate files while respecting intentional exclusions. Suppress known synthetic fixtures narrowly, never all tests or all environment files indiscriminately.
- Add local dependency vulnerability reporting, for example a pinned `govulncheck` plus npm's audit facilities. Treat network/scanner failure as unknown/failed, not clean.
- Triage existing findings separately from new ones. Do not promise an immediately clean vulnerability baseline or silently whitelist existing security debt. Record concrete follow-up and explicit exceptions where needed.
- Ensure personal agent settings, credentials, and temporary evidence are excluded by repository rules where appropriate, without depending on one developer's global Git ignore.
- Document that tool/package/skill additions can execute code and need review. Avoid adding a collection of third-party agent extensions.
- Carry forward the instruction that normal validation is test-only, not an invitation to start the live application with its usual configuration.

**Acceptance:**

- A harmless synthetic secret fixture verifies scanner behavior without involving real credentials.
- Security reports are actionable and do not print discovered secret values.
- The workflow describes the accepted host-credential risk accurately; no containment claim is made.

## 6. Ordering and scope control

Recommended order: Increment 1, Increment 2, Increment 3, then complete Increment 4 and Increment 5. Task/review guidance can be used as soon as Increment 1 lands.

Target total: roughly 3–4 focused days including verification and local review. The ranges depend on test repairs and security findings. If the budget is exhausted, defer optional reporting automation and broad cleanup before reducing test correctness or weakening checks to make them green.

Do not combine all increments into one large unreviewable diff. The owner controls when to commit and when to create a PR.

## 7. Explicitly deferred work

- Any change to local isolation, VM/container execution, host credential permissions, or global agent configuration.
- Automated GitHub publication, an agent GitHub identity/App, PR templates as the primary workflow, automatic merging, or deployment.
- Service discovery, registration, monitoring, and the proposed Prometheus/Grafana work.
- Full application simulators, browser E2E infrastructure, production login bypasses, and per-worktree full-stack environments.
- OpenAPI adoption, generated clients, broad architectural enforcement, and large service refactors.
- Repository-wide coverage thresholds, comprehensive mutation testing, and type-checking every existing frontend test if that requires substantial unrelated repair. The existing exclusion must remain documented until addressed.
- A custom agent orchestration platform, repository RAG/vector database, broad MCP setup, or large agent benchmark.
- Release architecture changes. The observed promotion input interpolation, mutable Action references, and release concurrency/authority deserve a separate hardening change; this milestone must not imply they were fixed.

## 8. Measuring outcomes without building infrastructure

This is ongoing adoption follow-up, not an additional tracking platform or proof already supplied by the implementation milestone. For approximately the next 10 substantive tasks, keep a lightweight local record as those tasks occur:

- Task type and rough size/risk.
- Harness/model used.
- Significant wrong assumptions found during implementation or review.
- Human review effort and rework requested.
- Whether a regression test was added and which local checks ran.
- Regressions discovered after merge.
- Available agent usage/cost, including review and rework rather than just implementation.

Use this as directional evidence, not a statistically conclusive model ranking. Tasks differ, subscription costs are not always attributable per call, and fewer injected characters do not guarantee proportionate billed savings because caching and retries vary.

Prefer improving tests and context routing before adding another reviewer. Keep manual model selection; do not route all tasks to a cheaper model merely to minimize per-call prices.

## 9. Milestone completion checklist

- [x] Root guidance/adapters validated structurally and shared skills discovered by installed Pi, Codex, and Claude Code (native discovery checks; no model behavior evaluation).
- [x] Critical rules no longer depend on reading only `CLAUDE.md`.
- [x] Automatic large-reference injection is removed and identified instruction/reference drift is corrected.
- [x] Clean bootstrap and the full local check command work.
- [x] All verification entrypoints have overall deadlines covering downloads, tool probes, builds, vet, and test setup as well as test execution; timeout/cancellation behavior has regression coverage.
- [x] Required test suites cannot silently succeed without running.
- [x] The stale service/database lifecycle test is repaired and selected by CI.
- [x] High-value regression gaps have new meaningful tests.
- [x] Local review produces concise final-state evidence, without PR publication.
- [x] Local security checks have an explicit result/triage path; existing dependency findings still fail and remain open.
- [x] No service versions, application changelogs, GitHub settings, host isolation, or release behavior were changed incidentally.
- [x] The accepted lack of technical containment remains explicit.

Completion means a better verified, lower-friction local agent workflow. It does not mean autonomous production authority, complete E2E coverage, or immunity to agent mistakes.

## 10. Research basis and limits

Research was reviewed on 2026-09-05. Vendor guidance provides useful operational patterns, not universal proof of productivity. Prefer observable repository outcomes over claims about how many lines an agent can generate.

- [OpenAI: Harness engineering](https://openai.com/index/harness-engineering/) — repository-local knowledge, progressive disclosure, executable invariants, and observable application behavior. Adopt the feedback-loop ideas, not its permissive merge policy.
- [Anthropic: Claude Code best practices](https://code.claude.com/docs/en/best-practices) — explicit verification, focused context, exploration before implementation, and independent review.
- [Agent Skills specification](https://agentskills.io/specification) — portable metadata and on-demand reference loading.
- [Evaluating AGENTS.md](https://arxiv.org/abs/2602.11988) — empirical caution against assuming additional repository instructions improve task success; effects depend on the tested setting.
- [Anthropic: Demystifying evaluations for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents) — verify end-state outcomes and use deterministic checks where possible.
- [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use) — least privilege, safe workflow inputs, and immutable Action references; broader release hardening is deferred here.
- [Anthropic: Sandboxing](https://www.anthropic.com/engineering/claude-code-sandboxing) — filesystem and network boundaries are security controls distinct from instructions; no new sandbox is being implemented by agreement.
- [Codex: AGENTS.md](https://developers.openai.com/codex/guides/agents-md) and [skills](https://developers.openai.com/codex/skills) — instruction/skill discovery and shared skill locations.
- [Claude Code: Memory](https://code.claude.com/docs/en/memory) and [skills](https://code.claude.com/docs/en/skills) — importing AGENTS.md and symlinked shared skill directories.
- Installed Pi documentation (`README.md`, `docs/skills.md`, `docs/settings.md` in the `@earendil-works/pi-coding-agent` package) — context discovery, shared `.agents/skills`, project trust, and the distinction between extensibility and isolation.

## 11. Implementation progress

### Step 1 — implemented locally, 2026-09-05

- Consolidated essential rules and navigation into a 104-line shared working guide. `CLAUDE.md` imports it rather than duplicating project facts.
- Added eight canonical skills in `.agents/skills/`, with relative directory links for Claude. Retained service/documentation/changelog entry points and added `squash-task`/`squash-review`. No model defaults or executable skill hooks were added.
- Removed the full-reference prompt hook and its sole-purpose project settings file. Personal/global settings and host isolation were not changed.
- Added focused architecture, invariant/test-gap, development, local workflow, and service references under `docs/`. Corrected the identified schema/configuration/booking/scheduler/package-tree drift in current references; historical assessment findings above remain historical.
- At the Step 1 boundary, commands and limitations remained explicit; the `make` interface and lifecycle-test repairs were intentionally deferred to Step 2.

Validation on installed versions:

| Check | Result |
|---|---|
| Pi 0.85.1 native `loadSkillsFromDir` | All eight source skills and all eight adapter paths parsed without diagnostics; metadata-only prompt, not full reference injection |
| Codex 0.153.4 app-server `skills/list` | All eight project skills discovered using a temporary Codex configuration; no model turn requested |
| Claude Code 2.1.261 initialization command listing | All eight linked skills discovered; hooks/MCP/tools disabled for this discovery check, no model turn requested |
| Static documentation validation | Local link/heading targets, named invariant test anchors, standard skill metadata, symlink-relative references, Claude root import, and hook removal checked |
| Diff scope and whitespace | No application code, tests, workflows, versions, migrations, or application changelogs changed; whitespace checks passed |

The previous six skill entry files totaled 143,292 bytes; the eight new entry files total 10,956 bytes. Detailed knowledge now loads through focused references. This is a context-size improvement, not a measured billing/productivity claim.

No application tests were rerun for this documentation/agent-resource-only step; baseline test results are recorded in section 3. Native resource discovery and static import checks do not establish behavioral adherence by every model. Restart existing agent sessions to load the new instructions; Pi project trust remains an owner decision. No commit, push, PR, or GitHub configuration change was performed.

### Step 2 — implemented locally, 2026-09-05

- Added a portable `Makefile` and small noninteractive `scripts/checks/` helpers for `doctor`, locked frontend bootstrap, fast/full verification, and separate vulnerability checks. Fast checks require current embedded assets but not Docker; full checks rebuild assets and run build, format/diff/vet/type/unit/frontend, race, PostgreSQL integration, and lifecycle verification.
- Changed all integration `TestMain` entry points to use one Docker availability check and fail explicitly when an explicitly requested Docker-backed suite cannot run.
- Repaired the historical `e2e`-tagged test against current canonical identity, constructor, participation, capacity, and repository contracts. It now uses the shared Testcontainers PostgreSQL helper instead of a fixed host port/Compose project and is documented as a service/database lifecycle test rather than browser/transport end-to-end coverage.
- Reused `make bootstrap` and `make check` in the existing `build-and-test` CI job while preserving the `build-and-test` and `frontend-test` job names expected by release verification.
- Added an explicit frontend application type-check command and resolved the assessment's four tracked `gofmt` findings without semantic changes. Updated setup, verification, service-test, invariant, and plan references to describe the implemented interface and remaining gaps.
- At the Step 2 boundary, `make check-security` pinned `govulncheck` and audited frontend npm dependencies. The prospective-change secret scan and explicit security-debt triage were deferred to Step 5.

Validation on the final affected code paths:

| Check | Result |
|---|---|
| `make bootstrap` followed by `make doctor` | Passed; locked install/build completed and all configured prerequisites/assets were reported ready. |
| `make check` | Passed, including build, fast checks, 107 frontend tests, race-enabled Go tests, Docker-backed integration suites, and `TestServiceDatabaseLifecycle`. |
| Docker-unavailable integration simulation | Confirmed an explicit nonzero exit and actionable `Docker CLI not found` diagnostic. |
| `make check-security` | Ran both checks and failed honestly: `govulncheck` reported 14 reachable advisories across 5 modules; npm audit reported 10 findings (1 low, 4 moderate, 5 high). Dependency upgrades/triage remain Increment 5 scope. |
| Static review | Shell/Make/JSON/YAML syntax, repository-wide Go formatting, diff whitespace, changed-document local paths, CI job names, and intended file scope checked. |

No runtime `.env`, live application service, Telegram/Eversports operation, application version, changelog, release, commit, push, PR, deployment, or GitHub setting change is part of this step.

### Step 3 — implemented locally, 2026-09-05

- Added focused Telegram handler regressions for live admin revalidation before publication, compatibility with legacy kick callbacks that carry Telegram IDs, and in-place announcement edits retaining the standard inline keyboard.
- Added the first dedicated Telegram management HTTP client test file. Representative contracts cover identity resolution, canonical `user_id` and actor propagation, local-offset timestamps, partial cancellation responses, sentinel conflicts, typed HTTP status/message errors, bearer authentication, and request/response shapes.
- Extended the Eversports client tests for two failure boundaries: an ambiguous step-1 gateway timeout is not retried, while failures in optional post-payment match/tracking work preserve the successful booking result without inventing a `matchId`.
- Kept the increment test-only apart from invariant/service/plan documentation; no production refactor or new dependency was needed. Remaining callback/route breadth and distributed exactly-once behavior stay explicitly partial or uncovered.

Validation on the final test/documentation state:

| Check | Result |
|---|---|
| Focused race run | `go test -race -count=1 -timeout 120s ./cmd/telegram/telegram ./cmd/telegram/client ./cmd/booking/eversports` passed. |
| Mutation probes | Removing each protected authorization, legacy callback translation, keyboard, canonical actor, and no-retry behavior made its focused test fail; production files were restored before final verification. |
| `make check` | Passed, including fast checks, 107 frontend tests, race-enabled Go tests, Docker-backed integration suites, and the service/database lifecycle test. |
| Static/document review | Go formatting, staged/unstaged whitespace, local documentation paths, intended test-only executable scope, and invariant anchors checked. |

No live Telegram/Eversports request, application startup, runtime `.env`, dependency, service version, changelog, commit, push, PR, release, or deployment is part of this step.

### Step 4 — implemented locally, 2026-09-05

- Tightened the shared task and review procedures around final-state evidence: every relevant check is reported as `PASS`, `FAIL`, or `NOT RUN`, and later edits invalidate affected evidence.
- Added copyable local handoff and fresh-review briefs covering the task, acceptance criteria, constraints, base revision, actual staged/unstaged/untracked scope, pre-existing exclusions, exact verification, decisions, and remaining risks. A summary or diffstat is explicitly not a substitute for the changed lines.
- Kept review findings focused on concrete scenarios, consequences, file locations, and useful regression checks. One optional review pass remains the substantive-task default after deterministic checks; trivial work needs none.
- Reused the already ignored `tmp/agent/` location for optional temporary evidence. No report generator, model invocation, paid review loop, commit, push, or PR automation was added.

Validation on the final procedural/documentation state:

| Check | Result |
|---|---|
| Workflow acceptance review | Handoff/review inputs, final-state result labels, diff scope, actionable finding requirements, optional-review policy, and containment caveat are explicit. |
| Static documentation validation | Local links and heading anchors resolve; skill metadata and Claude adapter links remain valid; `tmp/agent/` is ignored. |
| Diff scope and whitespace | Only shared task/review procedures and their plan/index references changed; whitespace checks passed. |

No application code or executable check target changed, so application tests were not rerun. No application startup, runtime `.env`, dependency, service version, changelog, commit, push, PR, release, deployment, GitHub setting, host permission, or global agent setting is part of this step.

### Step 5 — implemented locally, 2026-09-05

- Added `make check-secrets`, using pinned Gitleaks v8.30.0 against separate index/worktree snapshots and non-ignored untracked candidates. Matching is local, findings print locations/rule IDs rather than values, temporary snapshots are removed, and scanner errors cannot become a clean result. Private credential candidates fail without being read; test files and `.env.example` are not blanket-excluded. No project-wide suppressions were added.
- Extended `make check-security` to attempt secret, Go and npm checks independently with explicit result labels and bounded subprocesses. Retained govulncheck v1.7.0 and npm's high/critical exit threshold, made the lockfile-only npm scope explicit, and kept lower-severity findings visible. No automatic fix or dependency upgrade was added.
- Added offline Node/Git helper regressions to the fast/full check path and a separately selected real-Gitleaks synthetic smoke test. They use disposable local files/indexes, no accounts or Git commits.
- Added repository-local exclusions for personal agent settings, credential copies, session/package caches, retaining shared skill/adaptor and extension-source visibility. No existing personal settings or host configuration was read or changed.
- Added [security scope and triage](security-checks.md), recording existing Go/npm findings with concrete runtime and build/test follow-ups, no vulnerability waivers, and an explicit distinction between a threshold pass and zero findings. Updated setup/workflow guidance on executable tool/skill additions and the unchanged host-credential risk.

Validation on the final affected implementation:

| Check | Result |
|---|---|
| `node --test scripts/checks/*.test.mjs` | PASS: 13 offline helper tests, including distinct staged/worktree contents, exclusions, symlink handling, private candidates, scanner failures/redaction, dependency failures, and repository-only ignore rules. |
| `node --test scripts/checks/secrets.integration.mjs` | PASS: pinned Gitleaks detects synthetic staged/unstaged/untracked/template/test tokens without echoing their values; clean replacements pass. Inline allow-comments cannot bypass the scan. |
| `make check-secrets` | PASS: prospective local candidate scan; unchanged files and committed history are explicitly outside scope. |
| `make check-security` | FAIL as expected from existing debt: secret scan passes; Go reports 14 reachable advisories across 5 modules; npm reports 10 vulnerable-package findings (1 low, 4 moderate, 5 high). Both dependency failures remain visible and nonzero. |
| `make check` | PASS: helper regressions, build/format/diff/vet/type/unit checks, 107 frontend tests, race-enabled Go tests, PostgreSQL integration tests, and service/database lifecycle suite. |
| Static documentation/scope checks | Local links/anchors, shared skill adapters, shell/JavaScript syntax, whitespace, and intended scope checked. Service versions, changelogs, application dependency manifests/locks, and release workflows are unchanged. |

No real credential file, application startup, Telegram/Eversports operation, host/global setting change, application dependency upgrade, service version, changelog, Git commit, push, PR, release, deployment, or GitHub setting change is part of this step. Security debt is documented for owner-directed follow-up, not fixed or accepted by this milestone.

### Completion-audit follow-up — implemented locally, 2026-09-05

The audit of `06ca00f` found that Step 2's package/scanner timeouts did not cover prerequisite probes, installs, builds or vet. That remaining implementation gap is closed with a shared [Node/POSIX controller](../scripts/checks/run-bounded.mjs) around all six `make` entrypoints. The bootstrap commands moved into an internal shell recipe so installation and asset building share one deadline. Existing package/scanner limits and failure thresholds remain unchanged; no GNU `timeout` dependency, retry loop, model invocation or host isolation was introduced. [Deadline budgets, overrides and cancellation limits](development.md#deadlines-and-cancellation) are explicit.

Ten new offline regressions cover hung Docker/install/frontend-build/Go-build/vet commands, invalid limits, noninteractive execution, output/argv/exit propagation, cancellation and SIGTERM-resistant descendants. Each hung-tool fixture confirms the intended phase was reached. The doctor regression failed against the previous implementation before the fix. Timeout stays nonzero even if the command leader exits zero during termination; cleanup escalates only within the command's process group. The plan's historical trailing blank line was also removed.

Final-state verification:

| Check | Result |
|---|---|
| `node --test scripts/checks/*.test.mjs` | PASS: 23 offline helper tests, including the 10 deadline regressions. |
| Clean checkout with the local implementation applied | `make bootstrap`, `make doctor` and `make check` passed without an initial `.env`, frontend dependencies or generated assets. Full verification includes 23 helper tests, 107 frontend tests, Go race tests, PostgreSQL integration and the lifecycle suite. Source diff remained unchanged; the disposable checkout was removed. |
| `node --test scripts/checks/secrets.integration.mjs` and `make check-secrets` | PASS: synthetic real-scanner coverage and prospective local candidate scan; no claim about unchanged files or committed history. |
| `make check-security` | FAIL: existing 14 reachable Go advisories across 5 modules and 10 npm findings (1 low, 4 moderate, 5 high) remain visible. Secret scan passes; no debt was waived or dependencies upgraded. |
| Static checks and diff review | JavaScript/shell/Make syntax, local documentation links/anchors, shared adapters, and current/milestone-wide whitespace checked. Changes are limited to the verification interface, its regressions and documentation. |

The implementation checklist is complete, pending owner review of the local diff. Section 8's task-by-task outcome observations and the documented dependency remediation remain follow-up work, not completed measurements or a clean security claim. No application behavior, service version, changelog, release workflow, global setting, Git commit or publication was changed by this follow-up. Verification here is on macOS; native Linux execution and model-behavior evaluation were not performed for this follow-up.

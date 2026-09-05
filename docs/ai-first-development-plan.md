# AI-first development: local-first implementation plan

**Status:** Step 1 implemented locally (2026-09-05), ready for owner review. Steps 2–5 are not implemented. See [implementation progress](#11-implementation-progress).

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

For approximately the next 10 substantive tasks, keep a lightweight local record:

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
- [ ] Clean bootstrap and the full local check command work.
- [ ] Required test suites cannot silently succeed without running.
- [ ] The stale service/database lifecycle test is repaired and selected by CI.
- [ ] High-value regression gaps have new meaningful tests.
- [ ] Local review produces concise final-state evidence, without PR publication.
- [ ] Local security checks have an explicit result/triage path.
- [ ] No service versions, application changelogs, GitHub settings, host isolation, or release behavior were changed incidentally.
- [ ] The accepted lack of technical containment remains explicit.

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
- Current commands and limitations remain explicit. Step 2's `make` interface and lifecycle-test repairs do not exist yet.

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


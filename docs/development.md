# Development and verification

## Prerequisites and boundaries

- Go: use [go.mod](../go.mod) as the minimum version (currently 1.26.6). The four Docker builder stages use `golang:1.26.6-alpine`; CI reads the version from `go.mod`.
- Node: use [web/frontend/.node-version](../web/frontend/.node-version) (Node 22; Vite 8 requires 22.12+ within that line). npm dependencies are locked by `package-lock.json`.
- Docker: required by `make check`, integration-tagged tests, and the service/database lifecycle test. [SetupTestDB](../internal/testutil/testdb.go) starts disposable PostgreSQL 15 containers and applies embedded migrations.
- Internet access: may be required by bootstrap and security checks when npm/Go caches are empty or vulnerability databases must be queried.

Tests use local fakes/HTTP test servers and disposable databases. Application startup is different: configuration automatically loads `.env`, management runs migrations, authorizes with Telegram, starts cron, and can announce changelogs. None of the verification targets start application services or load the runtime `.env`. Do not start the application or use the [manual operator examples](../README.md#running-locally-without-docker) as routine verification without explicit authorization.

## Clean checkout sequence

From the repository root:

```bash
make bootstrap
make doctor
make check
```

`make bootstrap` runs the locked local frontend install (`npm ci`) and builds the ignored `web/frontend/dist` assets embedded by [web/embed.go](../web/embed.go). It can replace the checkout's `web/frontend/node_modules`, as `npm ci` normally does, but does not install host/global toolchains or touch credentials. The equivalent existing generation command, `go generate ./web/...`, remains available.

`make doctor` is read-only. It reports Go, Node, npm, Docker-daemon, and generated-asset status without reading or printing `.env`. A functioning Node runtime is required to enforce deadlines; if Node is missing, doctor reports that prerequisite and exits before running other probes. Running it before bootstrap in a clean checkout intentionally reports missing assets and returns nonzero; run bootstrap, then doctor to confirm readiness.

## Verification target contract

| Target | Checks and side effects |
|---|---|
| `make doctor` | Reports prerequisites and missing/potentially stale generated assets. Does not install, build, or start services. |
| `make bootstrap` | Installs locked frontend dependencies and builds embedded assets. Downloads may require internet. |
| `make check-fast` | Requires current embedded assets; checks repository-wide Go formatting, staged/unstaged diff whitespace, offline local-helper tests, Go vet, application TypeScript, Go unit tests, and frontend tests. It does not require Docker. |
| `make check` | Rebuilds frontend assets, builds all Go packages, runs `check-fast`, race-enabled Go tests, PostgreSQL integration tests, and the historical `e2e`-tagged service/database lifecycle test. Every required failure or unavailable Docker daemon returns nonzero. |
| `make check-secrets` | Runs pinned Gitleaks v8.30.0 on changed index/worktree and non-ignored untracked candidate files, with location-only output. Does not need frontend assets or Docker. |
| `make check-security` | Separately attempts the secret scan, pinned `govulncheck` v1.7.0, and npm's audit of all locked frontend dependencies. Reports each result and returns nonzero for findings at the configured threshold or incomplete verification. Downloads/advisory queries may require internet. |

Generated ignored build/test caches are expected. The targets do not format files, delete Docker resources they did not create, alter Git history, commit, push, or publish. Testcontainers cleanup removes only containers started by the tests.

Missing assets and stale frontend inputs are diagnosed before `check-fast`; use `make check` to rebuild them. Full verification distinguishes this prerequisite error from test failures through labeled steps. The Go test commands have 120-second package timeouts and all scripts set `CI=1` for noninteractive execution.

Security scope, tool requirements, synthetic regression commands, result handling, and open dependency findings are documented in [Local security checks and triage](security-checks.md). Security checks remain separate from `make check`; a green application test run does not imply a clean security result.

## Deadlines and cancellation

All documented `make` entrypoints run through the shared [bounded command controller](../scripts/checks/run-bounded.mjs). A deadline covers the **whole entrypoint**, including dependency downloads, tool startup, builds, vet, test setup/cleanup and nested shell helpers—not just test execution.

| Entrypoint | Default seconds | Make override |
|---|---:|---|
| `doctor` | 30 | `DOCTOR_TIMEOUT` |
| `bootstrap` | 600 | `BOOTSTRAP_TIMEOUT` |
| `check-fast` | 900 | `CHECK_FAST_TIMEOUT` |
| `check` | 1800 | `CHECK_TIMEOUT` |
| `check-secrets` | 600 | `CHECK_SECRETS_TIMEOUT` |
| `check-security` | 1200 | `CHECK_SECURITY_TIMEOUT` |

For a slow cold-cache run, an explicit override such as `make check CHECK_TIMEOUT=2400` is supported. Values must be finite, positive seconds, at most 86400; zero does not disable the deadline. Existing Go package and security subprocess timeouts still apply independently. The full check invokes the fast shell helper under the same controller, without starting a nested detached command group. Shell scripts in `scripts/checks/` are internal recipes; use the `make` entrypoints for these overall deadlines.

The controller provides closed stdin, `CI=1` and `GIT_TERMINAL_PROMPT=0`, passes arguments without shell interpretation, and preserves normal exit codes/output. Expiry produces a labeled failure and controller exit 124 (`make` also exits nonzero). It sends SIGTERM to its own POSIX command group, then SIGKILL after a one-second grace period, even if the command leader exits zero while descendants remain. SIGINT/SIGTERM cancellation follows the same cleanup path with exit 130/143. An overall timeout terminates the entrypoint rather than retrying or starting later stages.

This uses standard Node/POSIX facilities on macOS and Linux, not GNU `timeout` or a new dependency. Process-group cleanup is not containment: independently detached processes or a forcibly killed controller may escape cleanup. It does not signal unrelated process groups, stop the Docker daemon, or prune shared containers. Abruptly interrupted tests/scans can leave their own disposable resources or temporary snapshots; do not clean unrelated owner resources to compensate.

Offline [deadline regressions](../scripts/checks/run-bounded.test.mjs) run with the other helper tests in `make check-fast`/`make check`. They exercise hung Docker/install/build/vet commands, exit/output/argument propagation, invalid limits, cancellation, and SIGTERM-resistant descendants using synthetic disposable fixtures only.

## Focused checks

Choose packages/test names for the change rather than running every check after each edit:

```bash
go test -count=1 -timeout 120s ./cmd/management/service -run TestPublishGame
go test -count=1 -timeout 120s ./cmd/booking/eversports
npm --prefix web/frontend test -- src/components/GameCard.test.tsx
```

Direct database-backed commands remain supported and fail explicitly if Docker is unavailable:

```bash
go test -count=1 -tags integration -timeout 120s ./...
go test -count=1 -tags e2e -timeout 120s ./tests/e2e/...
```

The `e2e` tag is retained for command compatibility. Its maintained test validates migrations and a representative management service/storage lifecycle against PostgreSQL; it does **not** exercise a browser, HTTP service transport, Telegram, Eversports, or the complete user journey.

Frontend conventions and type-check exclusions are described in [Web](services/web.md#frontend-tests). Existing frontend test files remain excluded from application TypeScript checking; Vitest compiles and runs them separately.

## CI reuse

The required `build-and-test` job bootstraps and runs `make check`, including the lifecycle suite. The separately required `frontend-test` job remains intact for release-check compatibility. No release or publication behavior is part of these targets.

## Troubleshooting and honest reporting

- `make doctor` says assets are missing/stale: run `make bootstrap` for a clean/dependency setup, or `make check` to rebuild before full verification.
- Docker CLI missing/daemon unreachable: start/install Docker and rerun. Integration/lifecycle commands intentionally fail rather than report a success-shaped skip.
- Bootstrap/security download failure: report the check as blocked or failed, not passed or clean.
- A full check stopping at one labeled step means later steps did not run; report them as not run.
- Do not fix unrelated failures or weaken checks to get green output. Record them and keep task scope explicit.

## Documentation/configuration-only changes

Check local links and referenced paths, skill metadata and relative links, adapter discovery, and staged/unstaged/untracked diff scope. JSON/YAML/Make/shell syntax must parse if changed. Confirm no runtime code, versions, or application changelogs changed accidentally. Application test reruns are unnecessary when no executable application behavior changed; state this in the handoff.

Agent setup and discovery smoke-test expectations are in [Agent workflow](agent-workflow.md#tool-setup-and-discovery). Temporary reports can live under ignored `tmp/agent/`; do not commit session transcripts or secret-containing output.

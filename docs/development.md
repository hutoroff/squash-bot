# Development and verification

## Prerequisites and boundaries

- Go: use [go.mod](../go.mod) as the minimum version (currently 1.25).
- Node: use [web/frontend/.node-version](../web/frontend/.node-version); npm dependencies are locked by `package-lock.json`.
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

`make doctor` is read-only. It reports Go, Node, npm, Docker-daemon, and generated-asset status without reading or printing `.env`. Running it before bootstrap in a clean checkout intentionally reports missing assets and returns nonzero; run bootstrap, then doctor to confirm readiness.

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

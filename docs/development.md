# Development and verification

## Prerequisites and boundaries

- Go: use [go.mod](../go.mod) as the minimum version (currently 1.25).
- Node: use [web/frontend/.node-version](../web/frontend/.node-version); npm dependencies are locked by `package-lock.json`.
- Docker: required for integration tests; [SetupTestDB](../internal/testutil/testdb.go) starts a disposable PostgreSQL 15 container and applies embedded migrations.

No `Makefile` or unified check script exists yet. Those are Step 2 of the [development plan](ai-first-development-plan.md), not prerequisites you should invent or pretend to have run.

Tests use local fakes/HTTP test servers and disposable databases. Application startup is different: configuration automatically loads `.env`, management runs migrations, authorizes with Telegram, starts cron, and can announce changelogs. Do not start the application or use the [manual operator examples](../README.md#running-locally-without-docker) as routine verification without explicit authorization.

## Build preparation

From the repository root:

```bash
go generate ./web/...
```

This runs `npm ci && npm run build` in `web/frontend`. It requires dependency-download access when caches are empty and produces ignored `web/frontend/dist` assets. [web/embed.go](../web/embed.go) requires those assets even when compiling the Go web package for tests. A fresh checkout cannot run the whole Go suite first. Rebuild after frontend changes before validating the embedded binary.

## Focused checks

Choose packages/test names for the change, rather than running every check after each edit:

```bash
go test -count=1 -timeout 120s ./cmd/management/service -run TestPublishGame
go test -count=1 -timeout 120s ./cmd/booking/eversports
npm --prefix web/frontend test -- src/components/GameCard.test.tsx
```

Frontend conventions and type-check exclusions are described in [Web](services/web.md#frontend-tests).

## Broader local verification

After build preparation, for executable changes:

```bash
go build ./...
go vet ./...
go test -race -count=1 -timeout 120s ./...
go test -count=1 -tags integration -timeout 120s ./...
npm --prefix web/frontend test

git diff --check
git diff --cached --check
# Reports formatting drift without modifying tracked Go files:
git ls-files '*.go' | xargs gofmt -l
```

`go build ./...` does not replace the frontend build/type check. Report the actual checks run, including omissions, failures, and pre-existing drift. The current [CI workflow](../.github/workflows/ci.yml) builds frontend assets and runs Go unit/integration plus frontend tests; it does not select every check above.

## Known verification gaps (until Step 2 repairs them)

- Management service/storage/API integration `TestMain` implementations exit 0 if Docker is unavailable. Check `docker info` availability and test output. An exit code alone is insufficient; report skipped execution as **not verified**.
- `tests/e2e/environment_test.go` uses stale constructor/participation signatures and fails to compile. Compile-only reproduction: `go test -tags e2e -run '^$' -timeout 60s ./tests/e2e/...`. CI currently does not select it.
- That E2E-tagged test starts PostgreSQL and exercises service/storage code, not the complete browser/HTTP/Telegram system. Do not claim full end-to-end coverage after merely fixing compilation.
- Existing frontend test files are excluded from TypeScript checking; passing `npm run build` does not type-check them.
- Some packages have sparse behavior coverage. See [the invariant map](invariants.md) rather than inferring correctness from a green suite or a coverage percentage.

Do not fix unrelated failures or disable assertions to get green results. Record them and keep the task scope explicit.

## Documentation/configuration-only changes

Check local links and referenced paths, skill metadata and relative links, adapter discovery, and staged/unstaged/untracked diff scope. JSON settings must parse if changed. Confirm no runtime code, versions, or application changelogs changed accidentally. Application test reruns are unnecessary when no executable application behavior changed; state this in the handoff.

Agent setup and discovery smoke-test expectations are in [Agent workflow](agent-workflow.md#tool-setup-and-discovery). Temporary reports can live under ignored `tmp/agent/`; do not commit session transcripts or secret-containing output.

# Local security checks and triage

These checks detect accidental exposure and known vulnerable dependencies. They are **not a sandbox**, a credential-access control, or proof that the application is secure. Agents still run with the owner's host access. Normal verification remains test-only; do not start the application, read real credentials, or contact Telegram/Eversports.

## Commands and results

From the repository root:

```bash
make check-secrets    # prospective local files only; no frontend assets or Docker needed
make check-security   # secret scan, pinned govulncheck, and locked npm dependency audit
```

The helpers use the existing Node runtime and Git. Gitleaks is pinned to `github.com/zricethezav/gitleaks/v8@v8.30.0`; govulncheck remains pinned to `golang.org/x/vuln/cmd/govulncheck@v1.7.0`. `go run ...@version` uses Go's module/build caches without adding application dependencies or installing a global binary. Gitleaks requires Go 1.25.4 or newer; Go's normal `GOTOOLCHAIN=auto` may download a compatible toolchain when needed. No host settings are changed. Initial tool downloads and advisory queries require network access; secret matching itself is local and does not validate tokens online.

`make check-security` attempts all three checks even if one fails. Each result is labeled `PASS`, `FAIL`, or `NOT RUN`; any failure or missing prerequisite makes the aggregate command nonzero. Missing embedded frontend assets block only Go analysis, not the secret/npm checks. Use `make bootstrap` to prepare assets. Scanner/download/network errors, timeouts, and invalid reports are incomplete verification, never a clean result. Raw scanner error output is withheld because it can include sensitive data; diagnostics identify the failed tool and prerequisites to check.

- Go analysis reports reachable advisories using the current toolchain and default build tags. Any reported reachable vulnerability fails. It is not an audit of the host Docker daemon or every build-tag/tool combination.
- npm audits **all locked dependencies**, including development dependencies, via `--package-lock-only`. High/critical findings fail, preserving the existing threshold; low/moderate findings remain visible and open. A threshold pass is not a zero-finding claim. No `npm audit fix` or dependency upgrade is automatic.
- External scanner processes have five-minute limits (Gitleaks matching also has a two-minute limit). Git subprocesses have 30-second limits. Unsupported inputs and output-buffer exhaustion fail rather than silently passing.

## Secret-scan scope and privacy

The [candidate helper](../scripts/checks/secrets.mjs) scans complete files, not just added lines:

- Changed **index blobs** are separate from **unstaged worktree contents**. A staged secret remains detectable even if the worktree copy has already been cleaned or deleted.
- Non-ignored **untracked** files are candidates too. Standard Git exclusions apply to untracked files. Tracked candidates are not silently excluded just because an ignore pattern matches them.
- Renames scan their new contents; deletions have no surviving contents to scan. An empty candidate set is reported explicitly. **Unchanged files, committed history, and already-committed branch changes are not scanned.** Run this before committing; it is not a branch/history security audit.
- `.env.example` and test files remain eligible. Real `.env`/`.env.*` files (except `.env.example`), key-container files (`.pem`, `.key`, `.p12`, `.pfx`), and recognized personal agent settings/credential files cause a failure **without reading their contents** if they enter the candidate set. Remove accidental private candidates from the prospective change set yourself. A legitimate public certificate or differently named synthetic template needs an explicit scope/policy review, not a silent exclusion.
- Symlinks are scanned as link text, never followed to their targets. Unmerged indexes, submodule candidates, special files (e.g. FIFOs)/non-UTF-8 paths, and files above 32 MiB fail as incomplete scans. Gitleaks' rule/file-format limits still apply: binary formats may be skipped, archive traversal is disabled, and not every secret is detectable. Do not edit the index/worktree concurrently; rerun after any affected change.

Candidate snapshots are materialized in a private OS temporary directory and removed when the helper exits normally, including handled failures. Abrupt termination can leave that temporary directory behind. Only snapshot label, original path, line number and rule ID are printed for findings—never the matched value, source context or raw scanner diagnostics. No raw secret report is written to the checkout. Optional saved command output belongs under ignored `tmp/agent/`; do not publish transcripts or credentials.

The [configuration](../scripts/checks/gitleaks.toml) extends pinned Gitleaks defaults with **no project suppressions**. The helper supplies its configuration and an empty ignore file explicitly; checkout `.gitleaksignore` files and inline `gitleaks:allow` comments do not suppress candidates. If an existing synthetic fixture is flagged, verify it is actually synthetic and add only a reviewed rule-specific allowlist with **both** an exact file path and exact synthetic value (`condition = "AND"`). Never suppress all tests, all environment files, or an entire rule to get green output. A confirmed real secret requires owner-led removal and rotation/revocation; do not echo it or attempt to use it.

## Regression checks

```bash
node --test scripts/checks/*.test.mjs
node --test scripts/checks/secrets.integration.mjs
```

The first command uses disposable Git indexes, fake scanner responses, and synthetic files, without commits or network access; it also runs inside `make check-fast`/`make check`. The second explicitly downloads/runs pinned Gitleaks against fake tokens in an isolated temporary repository, confirms index/worktree/untracked/template/test detection, then checks clean replacements. It does not use an external account or contact a token issuer. Keep it separate from the offline edit loop.

## Existing dependency findings — 2026-09-05

**Baseline:** `76e050f`, Go 1.26.6, Node 22.17.0, npm 10.9.2, unchanged `go.mod`/`go.sum` and frontend lockfile. Commands: `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` and `npm --prefix web/frontend audit --package-lock-only --json`.

Observed: **14 reachable Go advisories across 5 modules** and **10 npm vulnerable-package findings** (1 low, 4 moderate, 5 high). npm counts packages, not distinct advisory IDs. These are existing findings, not new application dependency changes from Increment 5. Advisory databases change; this dated record is a triage starting point, **not a scanner allowlist or an approved risk acceptance**. Both dependency checks remain failing. No vulnerability exception has been granted.

### Go follow-up

| Affected module / observed version | Advisories | Triage and next action |
|---|---|---|
| `github.com/jackc/pgx/v5` 5.5.0 | [GO-2026-5004](https://pkg.go.dev/vuln/GO-2026-5004), [GO-2024-2606](https://pkg.go.dev/vuln/GO-2024-2606), [GO-2024-2567](https://pkg.go.dev/vuln/GO-2024-2567) | **Prioritize runtime database path.** Analyzer traces enter management storage/pool/query operations. Prepare a focused upgrade to at least 5.9.2 (highest reported fix); verify SQL/protocol compatibility and run storage/integration/lifecycle checks. Reachability is not proof that current query inputs meet every exploit precondition. |
| `golang.org/x/text` 0.34.0 | [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) | Runtime pgx connection normalization trace; malformed-input infinite-loop advisory. Upgrade to at least 0.39.0 in the database dependency follow-up, checking Go-version requirements. |
| `golang.org/x/crypto` 0.48.0 | [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355), [6354](https://pkg.go.dev/vuln/GO-2026-6354), [5020](https://pkg.go.dev/vuln/GO-2026-5020), [5019](https://pkg.go.dev/vuln/GO-2026-5019), [5018](https://pkg.go.dev/vuln/GO-2026-5018), [5017](https://pkg.go.dev/vuln/GO-2026-5017), [5013](https://pkg.go.dev/vuln/GO-2026-5013) | Reported SSH traces originate in Testcontainers setup/termination. Not established as a live application SSH exposure, but local tooling is not risk-free. Review the container-test dependency chain and upgrade to at least 0.56.0, checking toolchain compatibility. |
| `github.com/moby/go-archive` 0.2.0 | [GO-2026-6253](https://pkg.go.dev/vuln/GO-2026-6253) | Archive traversal advisory, with Testcontainers and conservative initialization/interface traces. Upgrade to at least 0.3.0 via a compatible test-container chain; verify disposable DB lifecycle and archive-input exposure. |
| `github.com/docker/docker` 28.5.2+incompatible | [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887), [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883) | AuthZ/plugin advisories; analyzer reports broad client/initialization traces, not demonstrated daemon exploitability. No fixed version was supplied for this module. Investigate upstream client/module migration through Testcontainers; do not upgrade the host daemon or suppress the module as a side effect. |

### Frontend/tooling follow-up

| npm finding(s) / observed version(s) | Triage and next action |
|---|---|
| `react-router-dom` 6.30.3, `react-router` 6.30.3, `@remix-run/router` 1.23.2 — moderate | **Runtime dependencies.** Review redirect/link/navigation inputs and SSR-hydration applicability; prepare a compatible router update and navigation/auth regressions. Includes [GHSA-2j2x-hqr9-3h42](https://github.com/advisories/GHSA-2j2x-hqr9-3h42), [GHSA-wrjc-x8rr-h8h6](https://github.com/advisories/GHSA-wrjc-x8rr-h8h6), [GHSA-337j-9hxr-rhxg](https://github.com/advisories/GHSA-337j-9hxr-rhxg), and [GHSA-jjmj-jmhj-qwj2](https://github.com/advisories/GHSA-jjmj-jmhj-qwj2). Do not assume npm's `fixAvailable` resolves every advisory without rerunning audit. |
| `vite` 5.4.21 and nested 8.0.8 — high; `esbuild` 0.21.5 and nested 0.28.0 — moderate | Development/test-server path traversal, file-read and platform-specific issues; these packages are not the embedded Go server. Do not expose local dev servers. npm proposes Vite 8.2.2, a major upgrade for the direct dependency: review Vite/plugin/Node compatibility and both dependency paths, then run frontend build/tests and `make check`. Representative reports: [Vite](https://github.com/advisories/GHSA-4w7w-66w2-5vf9), [esbuild](https://github.com/advisories/GHSA-67mh-4wv8-2f99). |
| `undici` 7.24.7 — high | Development/test dependency; multiple HTTP/proxy/cookie/cache advisories, including [GHSA-8xcm-r25x-g524](https://github.com/advisories/GHSA-8xcm-r25x-g524). Refresh the compatible test dependency chain to at least 7.29.0 and rerun component tests. |
| `postcss` 8.5.8 — high | Build-time source-map file reads and CSS-stringification issues; includes [GHSA-fxqj-rqcc-2cmp](https://github.com/advisories/GHSA-fxqj-rqcc-2cmp). Resolve a compatible version beyond the reported affected range (`<=8.5.22`); test asset builds, especially untrusted CSS/source-map inputs. |
| `nanoid` 3.3.11 — high | Generator loop/size advisories, including [GHSA-2v37-7h3g-55p8](https://github.com/advisories/GHSA-2v37-7h3g-55p8); development dependency. Resolve at least 3.3.18 through its parent chain and verify builds. |
| `browserslist` 4.28.2 — high | Query-cache memory growth and untrusted stats handling; includes [GHSA-c83g-rgw3-j3cx](https://github.com/advisories/GHSA-c83g-rgw3-j3cx). Refresh past the reported affected range (`<=4.28.6`) and validate browser-target/build behavior. |
| `@babel/core` 7.29.0 — low | Build-time arbitrary source-map file-read advisory [GHSA-4x5r-pxfx-6jf8](https://github.com/advisories/GHSA-4x5r-pxfx-6jf8). Resolve a compatible version above 7.29.0 and test transpilation. Remains open despite the npm exit threshold. |

The owner should schedule the runtime database/router work first and the build/test dependency refresh separately. All rows remain **open** until fixes and applicable verification are reviewed. Development-only classification is not a waiver: builds and tests execute on the same credential-bearing host.

## Handling subsequent results

1. Compare advisory ID, module/package version, dependency path and scanner/toolchain version with the dated baseline. Record newly introduced findings separately from pre-existing debt or newly published advisories affecting unchanged dependencies. Do not compare counts alone.
2. Keep a failing/incomplete check failing in the handoff. State the concrete follow-up and relevant tests; do not silently widen suppressions or change severity thresholds.
3. An owner-approved temporary exception, if ever necessary, must identify the exact advisory/path/version, rationale, owner and revisit condition/date. None is configured here; this document does not make a failing check pass.
4. Rerun affected security and deterministic checks after the final edit. Keep only sanitized evidence, and leave commits, publication, release and remediation authority with the owner.

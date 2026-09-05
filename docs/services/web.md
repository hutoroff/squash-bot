# Web service and frontend

The Go web service serves an embedded React SPA and authenticated proxies to management. It has no database dependency. [main.go](../../cmd/web/main.go) wires handlers; [handler.go](../../cmd/web/webserver/handler.go) registers routes and SPA fallback.

## Authentication and identity

[auth.go](../../cmd/web/webserver/auth.go) verifies the Telegram Login Widget HMAC and authentication timestamp, resolves the canonical identity through management, and issues an HS256 session cookie with a canonical `uid` claim.

- Identity-resolution failure must not issue a fabricated session or substitute a Telegram ID for user ID.
- Sessions do not contain authoritative owner roles. `/api/auth/me` reads management's current user/owner state; do not reintroduce a locally configured owner list.
- Old cookies without canonical user ID are rejected. A `PlayerID` can be absent until first participation.
- Cookies are HttpOnly/SameSite=Lax, with Secure derived from TLS or the trusted proxy's `X-Forwarded-Proto`. The proxy must overwrite that header; do not assume untrusted direct access is equivalent to a trusted proxy.
- Frontend auth bootstrap distinguishes 401 from network/upstream errors. A transient management failure should show retry, not silently log the user out.

## Proxy authorization

[mgmt_proxy.go](../../cmd/web/webserver/mgmt_proxy.go) holds shared proxy/identity/group-authorization helpers. Domain handlers live in `games.go`, `groups.go`, `venues.go`, `prefs.go`, `users.go`, and `audit.go`.

- Derive action identity from the JWT, never a user-supplied body/query ID. `decodeWithActor` overwrites actor fields.
- Group-admin routes use the caller's authorized group list from management. Server owners are already included by that lookup; do not add a second owner shortcut. Owner-only routes such as the auto-booking master switch instead forward the authenticated actor and rely on management's owner check.
- Venue mutations force group ID from the path. Scope-check a venue read before proxying to a route that is not itself group-scoped.
- Game-specific participation/read operations use management's access check; a session alone does not authorize arbitrary game IDs.
- Preference routes use the session's user ID, not a `{userID}` selected by the caller.
- Owner-only operations and audit visibility are enforced by management. Forward authenticated identity, not arbitrary client headers claiming to identify the caller.
- Preserve upstream error/partial-success semantics instead of mapping every non-200 to a generic failure.

Exact route and DTO shapes belong in handler/client code. When changing a response, inspect intermediate decode/re-encode structs as well as frontend types: an omitted `user_id` field can be silently dropped by a proxy.

## Frontend

[App.tsx](../../web/frontend/src/App.tsx) owns auth/bootstrap/routes; `components/` contains pages and tests; `api/` contains clients; [types.ts](../../web/frontend/src/types.ts) defines frontend response shapes.

The frontend uses React 18 with React Router 7's `react-router-dom` compatibility exports; it remains a client-rendered `BrowserRouter` app, not an SSR/data-router framework. Vite 8 and React plugin 6 use Rolldown/Oxc; the package is ESM, and the build config explicitly preserves Vite 5's JavaScript syntax targets rather than adopting newer default browser floors.

Current user identity is `user_id`, not a raw Telegram ID. Game roster rows may retain Telegram display information but must include `user_id` for current-user comparisons.

Server owners have a **Feature toggles** page for global defaults and group overrides. Both reads and writes delegate live owner authorization to management; the proxy overwrites actor identity from the session. Flag saves wait for the authoritative response and reload effective values; read/write errors show a reload state instead of inventing disabled values. Scope changes are disabled during a save. See the [canonical flag inventory](../feature-toggles.md).

Existing group/venue settings use optimistic updates and rollback. Preserve group/venue scope, last-owner conflict handling, independent in-flight saves, and the Web-only preventive cancellation fraction. Venue edits include per-sport units/player overrides. Do not replace the server's authorization with conditional navigation visibility.

[settingsLabels.ts](../../web/frontend/src/settingsLabels.ts) holds UI labels/helpers; event labels in [auditEvents.ts](../../web/frontend/src/auditEvents.ts) mirror backend event constants and are checked by a drift test. The UI is currently English-only.

[web/embed.go](../../web/embed.go) embeds `web/frontend/dist`; `make bootstrap` runs the locked install and frontend build (`go generate ./web/...` remains equivalent). `make check` rebuilds assets, while `make check-fast` diagnoses missing or potentially stale output before compiling the embedded Go package. Vite development alone is not an authenticated full-stack environment: the current [Vite config](../../web/frontend/vite.config.ts) has no management/web API proxy or fake login configuration.

## Frontend tests

Tests live beside components and use Vitest + Testing Library:

- Preserve `globals: true` in `vite.config.ts`: Testing Library registers cleanup through the global `afterEach`; removing it leaks DOM between tests.
- Existing API mocks keep `ApiError` inline with `vi.fn()` stubs so tests can `instanceof`-check it. Follow nearby mocks when changing API modules.
- Prefer role-based selectors when a heading and badge share text; do not weaken tests to avoid ambiguous `getByText` matches.
- Test files/setup are excluded from the application tsconfig. `tsc && vite build` does **not** type-check the tests; a passing Vitest run is not equivalent to a test type check.
- [auditEvents.test.ts](../../web/frontend/src/auditEvents.test.ts) reads backend audit constants and detects missing/extra labels. Run it after changing event types; also keep the TypeScript union consistent.

[App.test.tsx](../../web/frontend/src/App.test.tsx) exercises the real auth bootstrap, BrowserRouter, route table and Dashboard navigation with mocked page bodies: canonical identity/deep links, owner route visibility, 401 login, and transient-failure retry states. It is compatibility coverage, not browser E2E or a replacement for server authorization tests.

Backend regression anchors include `auth_test.go`, `games_test.go`, `groups_test.go`, `venues_test.go`, and `users_test.go`. Use local HTTP test servers and synthetic signed auth data. Do not add a production authentication bypass or rely on a real public tunnel for routine validation. See [Development](../development.md) for commands and [Invariants](../invariants.md) for known gaps.

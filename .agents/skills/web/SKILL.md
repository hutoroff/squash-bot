---
name: web
description: Use before changing web session/authentication, management proxies, React UI, or frontend tests in cmd/web and web/frontend.
---

# Web changes

1. Read [the web guide](../../../docs/services/web.md), including frontend test conventions.
2. Inspect the route, auth/group/game access gate, intermediate DTOs, frontend client, and nearby tests before changing a contract.
3. Check [invariants](../../../docs/invariants.md): JWT-derived identity, overwritten actor/group fields, live management owner authority, and authorization distinct from UI visibility.
4. Run focused backend/component tests. Rebuild embedded assets before verifying the Go web binary using [current commands](../../../docs/development.md).
5. Keep TypeScript's current test exclusions explicit; a frontend build is not a test type check. Do not add a production auth bypass for testing.

Update the focused reference and product README only where affected. Follow root AGENTS.md and the [local task procedure](../squash-task/SKILL.md).

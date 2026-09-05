---
name: telegram
description: Use before changing Telegram callbacks, commands, wizards, rendering, webhook/polling behavior, or the management HTTP client in cmd/telegram.
---

# Telegram changes

1. Read [the Telegram guide](../../../docs/services/telegram.md).
2. Inspect the router/handler, ManagementClient interface/implementation, and relevant fake/test before planning.
3. Check [invariants](../../../docs/invariants.md): canonical identity, per-action authorization, old callback payloads, in-place announcements, and keyboard behavior.
4. Do not assume sync.Map makes pointed-to wizard state sequential or that two message-edit workers share an ordering lock.
5. Run focused mocked tests using [current commands](../../../docs/development.md); do not register a webhook or start polling as a test.

Update the service reference for new non-obvious behavior. Follow root AGENTS.md and the [local task procedure](../squash-task/SKILL.md).

# Telegram service

The bot owns Telegram transport, command/callback handling, rendering, and in-memory conversation state. All application data operations go through management HTTP, not direct database access.

## Entry points and test seams

- [main.go](../../cmd/telegram/main.go): management version check, Bot API and bot wiring, webhook/polling selection.
- [bot.go](../../cmd/telegram/telegram/bot.go): update loop, identity/language helpers, state, handler concurrency and edit workers.
- [handlers.go](../../cmd/telegram/telegram/handlers.go): message/callback dispatch and text helpers.
- [callback_router.go](../../cmd/telegram/telegram/callback_router.go): authoritative action map; add actions here rather than extending an if-chain in the dispatcher.
- [ManagementClient](../../cmd/telegram/client/interface.go): bot-facing contract. Add/adjust methods here and in [client.go](../../cmd/telegram/client/client.go), then update relevant fakes.
- Domain handlers are beside the dispatcher: `participation_handlers.go`, `game_manage_handlers.go`, `newgame_handlers.go`, `venue_handlers.go`, `settings_handlers.go`, `result_handlers.go`, `leaderboard_handlers.go`.

Use nearby bot tests with HTTP/fake management dependencies. Do not start a real bot to exercise a callback. Configuration defaults live in [config.go](../../internal/config/config.go), not this reference.

## Transport and concurrency

[Webhook mode](../../cmd/telegram/telegram/webhook.go) registers an HTTPS public URL but binds a plain HTTP listener behind a TLS proxy. Incoming secrets are compared in constant time; config validation requires a sufficiently long secret when enabling webhook mode. Graceful shutdown does not delete the webhook.

Polling deletes an existing webhook before starting updates. Startup can fall back from failed webhook setup to polling. Running another process is therefore not a harmless way to inspect behavior: it can change the shared bot's transport registration.

Updates are processed concurrently with bounded handler concurrency. `sync.Map` protects the map operations, **not mutations of wizard objects stored as pointers**. Do not assume per-user sequential delivery. Wizard state is process-local and lost on restart; there is no distributed conversation coordination.

## Identity, permissions, and language

Resolve the Telegram user through `resolveUser` before canonical user-keyed management calls. `ResolvedUser.UserID` is the action/audit identity, `PlayerID` may be nil until first participation, and `DisplayName` is supplied by management. Do not revive the removed `GetPlayerByTelegramID`/`resolvePlayer` flow.

Admin-only callbacks must re-check rights, not trust that an earlier wizard step was authorized. Group administration comes from Telegram at runtime. Server-owner authority is managed by management, not a separate bot-local list.

Private messages use user DM language preference, then Telegram `LanguageCode`, normalized to en/de/ru. Group-visible messages use group language and timezone. The user-language cache is a local optimization, not identity authority; a cold lookup can resolve identity and read the user separately.

## Callback compatibility and state

Callback dispatch splits `action:rawID` at the first colon; individual handlers parse the remaining payload. Old Telegram keyboards persist after deploys, so payload changes require compatibility handling. The router is the action source of truth; do not maintain a second exhaustive list here.

- Message IDs are chat-scoped: composite keys must retain chat ID where needed.
- Any new slash command/conversation flow must preserve the established pending-state cleanup behavior.
- New-game selection includes sport and resolved venue units; single/multi-venue and single/multi-group paths differ.
- Venue edits use shared toggle/confirm and free-text state patterns. Preserve unrelated sport settings and the Web-only preventive cancellation fraction.
- Credential entry deletes the password message before API work; do not log or echo it. Credential controls depend on booking configuration and management encryption support.
- Court removal with active bookings requires pre-flight confirmation; read/lookup failures must not silently fall through to destructive edits. Partial cancellation has a distinct user outcome.
- Existing legacy kick callbacks have a fallback mapping the old Telegram-ID target to the current roster's player ID. Do not remove it as an unused ID convention without considering old keyboards.

## Rendering and message updates

[Shared game formatting](../../internal/gameformat/formatter.go) is used by both management and telegram. Keep announcements updated in place and preserve their inline keyboards.

`sendText`/`editText` use Markdown. Escape user-controlled names, titles, venue labels, and other interpolated text via the existing `escapeMarkdown` helper. **Do not escape inline-button labels**, which are not Markdown-parsed. Invalid markup can appear as a stalled wizard because some helpers suppress send errors.

Telegram mention offsets are UTF-16-based, not Go byte offsets. Preserve the mention tests when changing parsing.

Participation handlers enqueue coalesced per-game edits; the worker fetches fresh state and handles rate limits. It is process-local and independent from management's notifier locks. Do not infer global ordering or exactly-once messages from either mechanism.

## Results, errors, and tests

The result wizard uses canonical user/player identity, eligible games, opponent/winner/score selection, and an approval DM. At the optional score step, **Points** is selected by default and **Games/sets won** is an explicit alternative; changing type clears numeric input. Preserve `res_score_skip:_` and the new `res_score_kind:points`/`res_score_kind:games` callbacks. Skip clears score and kind. Preview and approval cards show the kind; untyped legacy results display an unknown type rather than guessing.

Score input is always author:opponent, not winner:loser. Shared validation requires 1–6 decimal digits per side, strictly higher winner score, or equal scores for a draw (`0:0` is a draw only). The localized en/de/ru guidance explains ordering and optional experimental rating impact. Management—not the bot—selects the effective [feature flag](../feature-toggles.md) at approval. Wizard state uses a mutex for concurrent score/type/skip handling; stale score callbacks cannot modify a preview. If the opponent cannot receive the DM, the newly submitted result is canceled rather than left to auto-approve. Preserve author/opponent authorization in subsequent callbacks and management calls.

Management result/leaderboard requests use `/api/v1/users/{userID}/...`, not old `/players/{tgID}` routes. Use the typed `HTTPError` and existing sentinel mappings for API failures; distinguish conflict/partial success from a generic error. Verify method contracts in the client rather than copying a request shape from a stale reference.

Nearby regression anchors include `commands_test.go`, `game_manage_handlers_test.go`, `venue_handlers_test.go`, `newgame_handlers_test.go`, `result_handlers_test.go`, `webhook_test.go`, and `bot_coalescer_test.go`. The dedicated [client contract tests](../../cmd/telegram/client/client_test.go) cover representative canonical identity/actor payloads, partial responses, and status mappings. These remain selected scenarios rather than exhaustive route or callback coverage. See [invariants](../invariants.md) and [verification](../development.md).

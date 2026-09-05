# Feature toggles

This is the canonical inventory of **registered framework flags**, not the live settings of a deployment. Registration and settings controls do not by themselves implement the consuming feature; activation status is documented below. Code defaults are always disabled; runtime overrides may explicitly enable a flag. Never read production configuration to maintain this document.

## Inventory

The registry/documentation consistency test checks the following table's keys, defaults, scopes, and service ownership.

| Key | Default | Scopes | Owning service |
|---|---|---|---|
| `rating.score_aware` | disabled | global, group | `management` |

Definitions: [shared registry](../internal/featureflags/flags.go). Persistence: [repository](../cmd/management/storage/feature_flag_repo.go), [migration](../migrations/037_feature_flags.up.sql). Authorization and audit: [service](../cmd/management/service/feature_flag_service.go).

## Managing and evaluating flags

Server owners use **Feature toggles** in the Web UI. Select Global defaults or a group, then choose Enabled, Disabled, or Default/Inherit. Only management's live DB-backed owner check grants permission; group administrators without owner authority cannot change flags. Settings persist across restarts. No migration/startup seed enables flags.

The management API provides `GET /api/v1/feature-flags` and `PATCH /api/v1/feature-flags/{key}`. Optional `group_id` selects a group; omit it for global scope. Reads require the authenticated transport's `X-Caller-User-Id`; writes require its canonical `actor_user_id` and display name. A write's `enabled` must be `true`, `false`, or `null` (remove override); omission is invalid. These APIs require internal bearer authentication. Web proxies derive identity from the signed session and overwrite client-supplied actors. See [management handlers](../cmd/management/api/feature_flags.go) and [web proxies](../cmd/web/webserver/feature_flags.go) for exact contracts.

Precedence is **explicit group override → explicit global setting → disabled code default**. Global Disabled is **not a kill switch**: a group's explicit Enabled still wins. Reset removes the override rather than writing false. Unknown keys and invalid scopes/values are rejected. Reads return the effective value and its source, alongside global/group overrides.

Configuration is read on demand, without a stale process cache. Missing rows use the disabled default. Database/read failures return an error, **not** a fabricated disabled value. A setting update takes effect for evaluations that observe the committed change; an operation already using its resolved policy is not restarted. Changes are audited best-effort, with actor, scope, and old/new override; audit delivery is not transactional with the setting write.

The framework registry/evaluation is reusable. The repository provides evaluation inside a management transaction; each consuming feature must explicitly wire its owning service to the evaluator. Booking does not acquire a new management dependency for a flag it does not use.

## `rating.score_aware`

**Status: registered, default disabled; rating consumer not connected yet.** The flag can be configured globally or per group, but changing it currently has no effect on result submission, scores, ratings, or rating history. The score-aware rating implementation is a separate change. Do not treat an Enabled setting as evidence that its consumer exists.

The later consumer must document its activation point, ordinary/score-aware behavior, failure handling, and rollback limitations here in the same change that connects it. No score schema, rating algorithm change, or historical recalculation is included in the feature-toggle framework.

## Verification

[Flag tests](../internal/featureflags/flags_test.go) enforce disabled defaults, precedence, and inventory consistency. [Database tests](../cmd/management/service/feature_flag_integration_test.go) cover persistence, override reset, and audit. [Management API tests](../cmd/management/api/feature_flags_test.go) cover owner authorization, invalid inputs, and failed reads; [Web proxy tests](../cmd/web/webserver/feature_flags_test.go) cover authenticated identity and denied access. These tests do not establish correctness of a future consuming feature.

## What is not a framework flag

Existing auto-booking permissions, venue switches, notification preferences, and result opt-out settings remain ordinary domain settings. They are not silently migrated into this framework and their existing defaults/behavior are unchanged. Their references remain in [management](services/management.md), [web](services/web.md), and the [operator README](../README.md).

## Required maintenance

Any task adding, changing, renaming, or removing a flag—or changing evaluation behavior—must update this document in the **same task**. Update the inventory, behavioral details, implementation/test links, activation rules, and rollback caveats as applicable. Do not describe proposed flags as implemented. Registry checks enforce structural consistency; agents must also review semantic accuracy. See [AGENTS.md](../AGENTS.md#keep-knowledge-current).

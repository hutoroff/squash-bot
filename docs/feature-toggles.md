# Feature toggles

This is the canonical inventory of **implemented framework flags**, not a list of planned features or the live settings of a deployment. Code defaults are always disabled; runtime overrides may explicitly enable a flag. Never read production configuration to maintain this document.

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

The framework registry/evaluation is reusable. The current flag is evaluated only by management; transport services must not independently decide rating policy. Booking does not acquire a new management dependency for a flag it does not use.

## `rating.score_aware`

**Purpose:** experimental, bounded margin weighting for all eligible 1v1 results (two players per unit), across supported sports. This is an extension to Glicko-2, not part of its standard specification.

### Enabled behavior

For a decisive, consistent result with explicit score kind `points` or `games`:

```text
margin = abs(author_score - opponent_score) / (author_score + opponent_score)
evidence_weight = 0.75 + 0.50 * margin
```

The [rating algorithm](../cmd/management/service/rating/glicko2.go) weights both the information and outcome terms. Win/loss outcomes stay 1/0; it does not substitute the proportion of points won. For a single decisive result the winner's update is positive and loser's negative (subject to numerical precision at extreme rating gaps). Close results have less influence than decisive ones at otherwise equal inputs. Different player uncertainties mean deltas need not sum to zero. One result still increments games played once, not once per point/game.

Examples: `11:9` weighs 0.800; `11:6` approximately 0.897; `11:3` approximately 1.036; `11:0` weighs 1.250. These are evidence weights, not final rating-delta multipliers. Both score kinds initially share this normalized policy; their statistical information is not assumed equivalent and their types remain recorded for future calibration.

### Ordinary behavior and fallbacks

- Disabled flag: ordinary Glicko-2 for every result.
- Missing/skipped score: ordinary Glicko-2, never a fabricated draw.
- Unknown/untyped legacy score: ordinary Glicko-2; never infer its type from the numbers.
- Inconsistent/invalid legacy score: ordinary Glicko-2, so pending historical results remain approvable.
- Draw, including `0:0`: ordinary Glicko-2.

New submissions use strict validation regardless of flag state: N:M is author:opponent; each side has 1–6 decimal digits; a winner must have the higher number; a draw requires equality. An omitted score kind remains compatible with old clients but is not eligible for weighting. These rules do not enforce every sport's official scoring system. See [score validation](../internal/resultscore/score.go) and [policy selection](../cmd/management/service/rating/score_policy.go).

### Activation, history, and rollback

[RatingService](../cmd/management/service/rating_service.go) resolves the flag **once at approval time**, inside the decision transaction. Both players use that policy and each other's pre-update ratings. Manual and 48-hour auto-approval share this path. Pending results therefore observe the setting at approval, not submission. A flag-read/rating/history-write failure rolls back approval and both rating updates.

Rating history records policy version (`glicko2-v1` or `glicko2-score-v1`), weight, explicit score kind, resolved enabled state, and selection/fallback reason. The linked immutable result holds the score and outcome. Existing history is marked legacy, not reinterpreted. These additions explain policy selection; they do not create a full historical rating replay tool.

Turning the flag off changes **future updates only**; it does not undo previously weighted updates or recalculate history. No historical backfill/recalculation is performed.

### Limitations and verification

Outcome-dependent margin weights can bias ratings, particularly when favorites win more decisively. Optional score omission also creates a selective-reporting incentive: an unscored win uses ordinary evidence even when a recorded close score would be downweighted. Opponent approval does not eliminate that incentive. The constants are conservative experimental starting values, **not calibrated claims of better prediction**. In the checked-in 10,000-match synthetic scenario, weighting moves the weakest/strongest players approximately −60/+52 points relative to ordinary ratings: a concrete illustration of spread inflation under that toy score model, not a prediction for real players. Leave the flag disabled until the owner reviews these trade-offs.

[Policy/numerical tests and a deterministic synthetic simulation](../cmd/management/service/rating/score_policy_test.go) exercise fallbacks, exact weight-1 compatibility, direction, margin ordering, and finite uncertainty. The simulation deliberately correlates winning margins with favorites and reports rating differences; it is not a statistical unbiasedness or predictive-accuracy test. [Flag database tests](../cmd/management/service/feature_flag_integration_test.go) cover persistence, overrides, and audit. [Rating database tests](../cmd/management/service/rating_policy_integration_test.go) cover approval-time selection, rollback, and concurrent decisions. [Flag tests](../internal/featureflags/flags_test.go) enforce disabled defaults and inventory consistency.

## What is not a framework flag

Existing auto-booking permissions, venue switches, notification preferences, and result opt-out settings remain ordinary domain settings. They are not silently migrated into this framework and their existing defaults/behavior are unchanged. Their references remain in [management](services/management.md), [web](services/web.md), and the [operator README](../README.md).

## Required maintenance

Any task adding, changing, renaming, or removing a flag—or changing evaluation behavior—must update this document in the **same task**. Update the inventory, behavioral details, implementation/test links, activation rules, and rollback caveats as applicable. Do not describe proposed flags as implemented. Registry checks enforce structural consistency; agents must also review semantic accuracy. See [AGENTS.md](../AGENTS.md#keep-knowledge-current).

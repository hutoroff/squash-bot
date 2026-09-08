# Management scheduling and booking

Read this for cron timing, publication, booking/cancellation, or failure semantics; general management ownership is in [Management](management.md).

## Jobs and timing

[main.go](../../cmd/management/main.go) constructs eight jobs and registers the poll plus a separate audit-retention cron. [Scheduler](../../cmd/management/service/scheduler.go) dispatches jobs sequentially within a poll; it does not provide a distributed lock or a guarantee against overlapping runs.

| Job/source (`cmd/management/service/`) | Timing / dedup to inspect |
|---|---|
| [CancellationReminderJob](../../cmd/management/service/cancellation_reminder.go) | Around game start minus grace period minus 6h; `notified_day_before` |
| [HalfwayCourtCheckJob](../../cmd/management/service/halfway_court_check.go) | First eligible poll at/after configured fraction of booking-open-to-grace-deadline; `halfway_court_check_done` |
| [FinalCourtCheckJob](../../cmd/management/service/final_court_check.go) | Around 15 minutes before grace deadline; `final_court_check_done` |
| [BookingReminderJob](../../cmd/management/service/booking_reminder.go) | 10:00–10:05 group-local; venue `last_booking_reminder_at` |
| [DayAfterCleanupJob](../../cmd/management/service/day_after_cleanup.go) | 03:00–03:05 group-local; game `completed` |
| [AutoBookingJob](../../cmd/management/service/auto_booking.go) | 00:00–00:05 group-local; existing result for venue/date/time prevents sequential rebooking |
| [AutoApproveResultsJob](../../cmd/management/service/auto_approve_results.go) | Every poll; pending results older than 48h |
| [PostLeaderboardJob](../../cmd/management/service/post_leaderboard.go) | Yesterday's group-local day, gated by 24h after last relevant game start; `last_leaderboard_posted_for` |

Read each job's actual eligibility and completion-marker logic before changing it; not all jobs use `game_days`, identical time windows, or identical retry policies. Timing helpers distinguish service-default timezone fallback from cancellation paths that skip external actions if group timezone cannot be established.

The preventive check anchors booking opening to **group-local midnight on the game date minus booking-opens days**, not `court_bookings.created_at`. Fraction choices are `1/3`, `1/2`, `2/3`; it releases half of currently unneeded courts, rounded down. Do not turn it into an all-unused-courts cancellation. Read the published-game, active-booking, and grace-deadline gates in the job and its tests.

## Booking and publication lifecycle

1. Auto-booking requires group permission, eligible venue/squash configuration, preferred time slots, and a positive `AutoBookingCourtsCount`.
2. Iterate **all configured preferred slots**. The court count limits courts per slot, not the number of games. A result lookup error skips that slot conservatively; an existing result skips it as already actioned.
3. [bookFreeCourts](../../cmd/management/service/booking_core.go), also used by manual booking, loads usable credentials, queries courts/occupancy, orders candidate courts, and books with per-credential limits.
4. Save per-court booking records with credential and time slot when a match ID is available. In reliable mode, atomically upsert one per-slot result and one linked **unpublished** game keyed by durable intent; replay can extend a partial result but cannot duplicate either row.
5. Booking reminder publishes through `GameService.PublishGame`; already-published games are skipped. Legacy result rows without a linked game retain a create-and-announce path.
6. Cleanup unpins/removes keyboards for published games, completes games, and closes booking rows; inspect its venue/date scope before changing multiple-session behavior.

`last_auto_booking_at` is recorded after any booked slot; it is **not the actual per-slot deduplication gate**. A unique DB result key protects stored rows, not external operations from concurrent processes or a crash between booking and persistence. Do not claim exactly-once booking.

Court selection uses numbers extracted from names, not Eversports IDs. Preferred court order drives booking; cancellation reverses it. Existing filtering has fallbacks when configured numbers do not match discovered names; read `filterFreeCourts` before tightening behavior.

Credential failures trigger cooldown/rotation. **Known limitation:** `bookFreeCourts` can classify a general booking error as credential failure and put a court back for the next account. An ambiguous timeout may already have caused a booking. The configured production path now consumes durable high-level/per-operation intents and the typed keyed-v2 client. It marks cooldown only for durable `credential_rejected` plus `no_effect`; transport, dependency, account/resource conflict, pending, and unknown outcomes do not poison credentials or authorize a changed key/account retry. The legacy branch remains only as a unit-test compatibility seam and the reliable read client rejects any accidental keyless mutation before HTTP. The upstream client's narrower retry protections are described in [Booking](booking.md).

Booking/persistence partial failures can leave external state without all local records. Notification behavior distinguishes no usable credentials/per-credential failures (audible admin DMs) from several other outcomes (silent). Do not flatten all errors to one retry or notification policy.

## Cancellation and manual court edits

[Scheduler cancellation](../../cmd/management/service/court_cancellation.go) uses stored booking entries and their credentials. Missing credentials skip the court and report an error. The old `cancelUsingListMatches` function now **returns an error**; there is no working service-level account fallback.

Prefer linked-result time-slot scoping; current legacy/error paths can fall back to venue/date reads. This is not unconditional cross-session isolation. Inspect `loadCourtBookingEntries` when changing cancellation or result linkage.

Scheduler selection uses reverse preferred order, then consecutive groups. Successful external cancellations are marked locally and removed from the game's courts; partial failures are collected and surfaced to admins. Do not equate a marker update with exactly-once external execution.

Manual removal in [GameService](../../cmd/management/service/game_service.go) differs: it computes a multiset diff, scopes active bookings, attempts cancellation, and persists the requested new courts even when some cancellations fail. The API can return partial-success details; Telegram presents a pre-flight confirmation. Preserve this distinction rather than treating manual and scheduled cancellation as identical.

Manual booking also returns requested/booked/failure details and edits the existing announcement asynchronously. Publishing a game and editing an existing announcement are separate operations.

## Reliable caller integration status

Management now stores stable business request keys, requested counts, UUIDv7 operation identities, immutable provider/facility scope and selected-court/credential details before dispatch. Scheduled and Telegram manual create/cancel paths use keyed v2, persist bounded status/evidence/warnings and original court-booking origin, poll accepted operations within a bounded budget, and recover unfinished same-key operations on later management polls. Manual command identities are propagated from Telegram callback IDs. Scheduler cancellation changes only confirmed cancelled courts; manual removal still persists requested edits and reports per-court failures.

Unknown/pending creation consumes requested capacity and cannot rotate credentials or select another court. Positively proven pre-admission conflict is retained as `not_dispatched`; only typed credential rejection with no-effect evidence enters cooldown. A separate explicit duplicate-risk allocator enforces one child of an unknown root and is not connected to ordinary automation or an HTTP authorization route, so the default dispatch policy remains disabled.

Production rollout is still blocked on the remaining acceptance work: legacy booking inventory/import, static combined-candidate review, and the runbook backup/restore/cutover drill. Durable bounded administrator notifications/aggregate metrics and idempotent auto-booking result/game materialization are now implemented. Discovery-aware booking invocation is implemented behind explicit `DISCOVERY_MODE=registry`; static keyed-v2 routing remains the default and production rollout gates are unchanged.

## Regression anchors

See `auto_booking_test.go`, `booking_reminder_test.go`, `scheduler_cancellation_test.go`, `halfway_court_check_test.go`, `final_court_check_test.go`, `game_service_book_test.go`, and `game_service_courts_test.go` beside the services. Include local-day/DST boundaries, partial external success, dedup errors, and marker behavior where relevant. Existing mocks do not prove live upstream idempotency; [the invariant map](../invariants.md) records that gap.

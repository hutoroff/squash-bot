# Reliable booking cutover, rollback, and recovery runbook

This is the operational handoff for the implemented booking reliability foundation. It does **not** authorize a deployment or a real Eversports mutation. A static management keyed-v2 integration candidate now persists and replays durable create/cancel operations, but production mutation cutover remains blocked until the remaining [management integration gates](service-discovery-design.md#101-management-integration-with-the-reliable-booking-contract), combined review, inventory/import, and this runbook's drills pass.

The booking service is replaceable but single-active. PostgreSQL and Eversports remain required dependencies; discovery registration is not implemented and is not an execution lock.

## 1. Hard gates

Do not begin production mutation cutover unless every item is true:

- Management persists its business intent and immutable operation UUID before dispatch for scheduled/manual creation and every cancellation path.
- Management requires `eversports-booking` major 2 create/cancel/status semantics and has no keyless/v1 mutation fallback.
- Management handles `202`, status replay, no-effect, warning, unknown, conflict, expired-result, and positively typed credential-rejection outcomes according to [§10.1](service-discovery-design.md#101-management-integration-with-the-reliable-booking-contract).
- Active legacy provider bookings have been inventoried. Verifiable records are imported through the keyed v2 import API; unresolved gaps stay explicit and block unsafe assumptions.
- One booking instance is configured. No rollout mechanism can overlap active executors.
- A fresh database backup and a successful disposable restore drill exist.
- The fake-provider restart/authority-loss drill and the repository's full checks pass on the exact candidate.
- An owner has scheduled a mutation pause and approved the cutover.

`BOOKING_RELIABLE_CUTOVER_ACK=keyed-v2-only` is an explicit startup acknowledgement that booking rejects keyless v1 mutations. Leave it unset until the management gates above pass. The acknowledgement prevents accidental startup with old assumptions; it does **not** prove management compatibility by itself.

## 2. Required configuration

Keep these secrets out of commands, logs, tickets, and metrics:

- `BOOKING_DATABASE_URL`: booking's direct connection to the existing PostgreSQL database/schema.
- `BOOKING_IDENTITY_KEY`: exactly 32 random bytes encoded as 64 hex characters. Preserve it with protected operational configuration; changing or losing it against an existing ledger fails startup.
- `INTERNAL_API_SECRET`: business API bearer.
- `BOOKING_MONITORING_SECRET`: distinct monitoring-only bearer.
- `BOOKING_RELIABLE_CUTOVER_ACK=keyed-v2-only`: set only after the hard gates pass.

Also configure a stable `BOOKING_SERVICE_KEY` and explicit Eversports facility ID, UUID, slug, and squash sport UUID. Different facility/sport pools must not share one service key. Production Compose keeps the admin port internal and gives the default 100-second drain a 110-second stop grace.

## 3. Safe pre-production drills

Use only repository fakes and disposable PostgreSQL:

```bash
go test -count=1 -tags integration -timeout 120s ./cmd/booking \
  -run TestReliableRuntimeStartupFakeProviderSingletonAndShutdown
make check
make check-secrets
make check-security
```

The runtime drill proves, without Eversports access:

1. booking-only migrations and identity-key verification complete;
2. one singleton starts and a second is rejected;
3. a fake-provider create executes once;
4. orderly stop and restart preserve same-key replay without another reserve;
5. stopping disposable PostgreSQL makes database/singleton readiness and admission fail closed; and
6. shutdown completes after authority loss.

It does not prove Eversports settlement, idempotency, visibility, or multi-replica safety.

## 4. Backup and disposable restore drill

Because management and booking share one database/schema, prefer a transactionally consistent backup of the **whole database**, including both migration histories. A booking-table-only export is easy to make inconsistent with management's local references and is not the default recovery artifact.

Operator-authorized example using protected `pg_service.conf` entries so credentials are not placed in command arguments. Prepare the empty disposable target separately, then point `booking-restore` only at that target:

```bash
PGSERVICE=booking-production pg_dump --format=custom --no-owner --no-acl \
  --file=<protected-path>/squash_bot.dump
PGSERVICE=booking-restore pg_restore --exit-on-error --no-owner --no-acl \
  --dbname=squash_bot_restore <protected-path>/squash_bot.dump
```

Never use production as the restore-drill target. On the disposable restore:

- inspect both management and `booking_schema_migrations` histories;
- run booking startup/integration checks with a disposable identity key only when the restored ledger is empty, or with the matching protected key when validating a real protected backup under explicit authorization;
- verify operation/import counts, unresolved outcomes, active claims, provider ownership, and tombstones are present;
- confirm an already-recorded operation key still conflicts/replays as expected; and
- destroy only the disposable restore resources when finished.

A stale backup can omit provider effects that happened after the snapshot. After any real restore, keep booking mutations paused. Compare durable state with authorized provider evidence, import only exact verified active bookings, retain unresolved gaps, and obtain a separate owner decision before resuming. Never treat an empty restored ledger as proof that the facility has no bookings.

## 5. Coordinated cutover

1. Announce and begin the mutation pause. Stop scheduled and manual booking/cancellation dispatch from old management callers.
2. Let known in-flight work finish. Investigate unresolved external outcomes; do not infer no-effect from timeout, process loss, or missing local rows.
3. Take the final protected backup.
4. Deploy configuration and migrations without starting keyless callers. Keep one booking instance only.
5. Verify the authenticated admin listener:
   - `/health/live` responds;
   - `/health/ready` reports database, singleton, and coordinator true;
   - `/descriptor` reports `eversports-booking` major 2 and the intended logical scope;
   - `/metrics` exposes bounded health, running, unknown-age, risk, HTTP, execution, and outcome series.
6. Import exact verified legacy provider evidence. Do not synthesize successful create operations or import ambiguous/non-owned matches.
7. Start upgraded management using static v2 invocation. Verify descriptor/contract compatibility before enabling mutation jobs.
8. Exercise same-key replay and status using fakes or an owner-authorized non-mutating check. A real booking/cancellation requires separate explicit authorization.
9. End the mutation pause only after management intent persistence, local result upserts, warnings/unknown handling, and cancellation ownership are observed working.

Management may remain available for non-booking functions while booking is unavailable. Discovery registration can be attached later and must not change the v2 safety boundary.

## 6. Normal replacement and shutdown

- Mark future discovery state draining when a registrar exists; today, stop routing callers first.
- Close admission before stopping the business listener.
- Keep the admin listener available while tracked execution drains.
- Allow the configured execution/finalization budget to finish; do not shorten Compose grace below it.
- Release singleton authority only after tracked execution stops, then close the pool.
- Start the replacement only after the old process has stopped. If overlap occurs, the second process must fail singleton acquisition rather than serve mutations.
- On restart, recovery classifies durable evidence only and never replays provider mutations.

A caller that received `202` or lost its HTTP response reads/re-submits the **same immutable key**. It must not create a new key unless the explicit, linked duplicate-risk policy has been durably authorized.

### Manual duplicate-risk exception (no automatic replacement)

The owner-only web-session API `POST /api/booking/operations/{id}/authorize-risk` has **no UI** and is disabled when management lacks a distinct, 32-byte-or-longer `JWT_SECRET` shared with web or reliable booking is not configured. This route requires an exact same-origin browser `Origin`, the signed session cookie, and a JSON decision with `expected_revision` (from inspection), `credential_id` (the explicitly selected credential for the **same venue**), a nonempty bounded `reason`, and `accept_duplicate_risk: true`. Management independently verifies the signed canonical user ID and current server-owner status; an internal bearer alone or a supplied actor ID is not sufficient. This is a real provider mutation path: do not use it for routine testing or a production drill without separate owner authorization.

Before authorizing, inspect the original key using authenticated management operation inspection and the booking ledger, confirm possible duplicate liability, and choose an account intentionally. The policy will reject if the original executor is still active, exact scope/court/time or parent revision differs, a credentialed read can reconcile a confirmed/no-effect result, or the original manual game has begun. Scheduled risk children are allowed only within the original venue-local `[00:00, 00:05)` opening window. It never overrides cancellations. A confirmed child does **not** resolve its unknown parent: inspect/reconcile each key and retain separate provider evidence and possible liability. Concurrent matching authorization yields the existing child, never another provider dispatch; an allocated but unsubmitted child remains inspectable and recovery must not submit it later. If the HTTP response is lost, inspect the same parent and linked child before attempting anything else. Never treat `202` as a successful court booking.

## 7. Failure response

### Readiness or database failure

Keep mutations disabled. Inspect authenticated readiness and bounded metrics without printing connection strings or tokens. Singleton/database loss permanently closes that process incarnation; fix the dependency and replace the process rather than trying to reacquire authority in place.

### Unknown provider outcome

Keep its account/resource claims blocked. Use credentialed read-only reconciliation only when an exact known match ID exists. Keyed resolution requires exact provider verification, expected revision, actor, and reason. Empty, delayed, mismatched, non-owned, or ambiguous reads remain unresolved.

### Identity-key mismatch

Do not generate a replacement key or clear metadata. Restore the protected matching key or perform a separately designed identity migration. Changing the marker would split account coordination.

### Multiple booking processes

Stop the non-authoritative process. Do not remove the advisory lock or terminate the authoritative database session merely to make the second process ready. Confirm no tracked executor remains before replacement.

## 8. Rollback

Preferred rollback changes routing/configuration while retaining the reliable booking binary, v2 contract, ledger, identity key, and evidence.

If application rollback would require an old keyless booking binary or old management client:

1. keep all booking/cancellation mutations paused;
2. do not set or bypass the cutover acknowledgement to make legacy traffic work;
3. retain compatible v2 status/reconciliation/import access for outstanding operations;
4. preserve all `booking_*` tables, migration history, identity key, claims, evidence, imports, and tombstones;
5. resolve or explicitly carry forward unknown/in-flight operations; and
6. require a new reviewed recovery/cutover plan before mutation resumption.

Never drop booking tables, reset operation keys, release claims by TTL, reinterpret unknown as no-effect, or roll back only one side into a v1/v2 mutation mismatch. Static routing is a supported rollback; unsafe keyless mutation is not.

## 9. Monitoring and evidence to retain

Alert on readiness loss, singleton/admission loss, database failure, unknown-operation count/age, repeated capacity rejection, and unexpected risk-replacement growth. IDs, accounts, credentials, provider-user IDs, raw provider bodies, and operation payloads must not become metric labels or alert text.

Retain the candidate revision, configuration names (not values), backup identifier, disposable restore result, exact check commands/results, migration versions, cutover timestamps, mutation-pause window, imported operation IDs in protected audit records, and unresolved-operation decisions. Do not store secrets or raw provider responses in the handoff record.

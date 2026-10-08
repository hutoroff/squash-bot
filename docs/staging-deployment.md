# Dedicated staging deployment (local images; no registry release)

This is an **opt-in deployment procedure**, not a test target or Step 14 cutover approval. It requires a dedicated Linux Docker host with a clean checkout of the candidate revision. **Do not run it on the production Docker host, on the owner's Mac, or against the development/production Compose projects.** It has not been exercised against a running staging environment. Build/test it locally with synthetic config first; the operator must review the runtime boundaries and authorize any staging startup.

## One command, after provisioning

On the dedicated host, with Docker Compose supporting `interface_name` (v2.36+), Node 22, and Git installed, provision a **private, owner-readable-only** environment file outside the checkout and a separate private Prometheus bearer file. Do not copy production `.env`, a production DB, bot token, booking identity key, credentials, or provider facility data. The private file must contain:

| Variable | Staging-only value |
|---|---|
| `POSTGRES_PASSWORD` | Long **URL-safe** password (hex works; used in PostgreSQL URL). |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_BOT_NAME` | Separate staging bot token and username (name without `@`). Starting management/Telegram contacts the real Telegram API **for this staging bot**, and Telegram polls; do not start without authorization for that bot. |
| `INTERNAL_API_SECRET`, `MONITORING_TOKEN`, `JWT_SECRET` | Independent random staging secrets, at least 32 characters each; monitoring token and internal secret must differ. |
| `BOOKING_IDENTITY_KEY` | New staging-only 64-hex-character key. Preserve it for the lifetime of the staging booking database. |
| `BOOKING_SERVICE_KEY` | Synthetic logical key such as `eversports.staging.squash`. |
| `EVERSPORTS_FACILITY_ID`, `EVERSPORTS_FACILITY_UUID`, `EVERSPORTS_FACILITY_SLUG`, `EVERSPORTS_SQUASH_SPORT_UUID` | Synthetic, correctly formatted facility/sport identifiers. **Never** use real inventory or provider credentials. |
| `STAGING_BACKEND_SUBNET` | An available private `/24` that does not overlap host/VPN/other Docker networks. This is also management's registry allowlist. |
| `STAGING_PROMETHEUS_TOKEN_FILE` | Absolute path outside the checkout to a file containing *only* the matching `MONITORING_TOKEN` (no newline), with private permissions. Compose bind-mounts it read-only. |

The optional `STAGING_WEB_PORT` and `STAGING_PROMETHEUS_PORT` default to host-loopback `18082` and `19090`; choose unused ports. Protect the private files (`chmod 600`) and do not paste rendered `docker compose config` output into tickets or logs: it includes secrets. This repository intentionally has **no** checked-in staging credential file. The script rejects an env file inside the checkout and a dirty Git tree; `.dockerignore` excludes `.env` files from application images.

After an operator confirms this really is a dedicated staging host, run **one command from the checkout**:

```sh
STAGING_DEDICATED_HOST=yes STAGING_ENV_FILE=/absolute/private/staging.env make staging-up
```

The command checks for a local default Docker context using `docker context ls` (it does not require the newer `docker context show` subcommand), validates Compose without printing it, tags and builds all four images at the committed Git SHA, and runs `docker compose --project-name squash-staging ... up --build --detach --wait`. **It never pushes application images**; the build can download public base images and Compose can pull pinned public PostgreSQL/Prometheus images. A failure leaves the staging containers/volumes for inspection, with no automatic retry, rollback, reset, or prune. `--wait` only checks configured container health/running status; it does **not** prove registry readiness, Prometheus scrapes, provider behavior, notification delivery, or Step 14 acceptance.

Before staging startup, offline synthetic validation is available:

```sh
make staging-config-check
node --test scripts/checks/staging-up.test.mjs
```

Neither command starts application services or reads private credentials. `make staging-config-check` needs the Docker Compose CLI, but not its daemon.

## Enforced boundaries and remaining risks

`docker-compose.staging.yml` creates separate project-scoped database/monitoring volumes, a private internal backend subnet, and an egress bridge **only** for management and Telegram. The booking container is backend-only, with no published ports or default Internet route; management has its booking URL and key forcibly empty, so it cannot dispatch real booking/cancellation operations. All application containers disable Watchtower, and no staging service is attached to the production `squash_services` network. Web and Prometheus publish on **host loopback only**; use a separately approved SSH tunnel/reverse proxy for remote access. No Grafana or backup/restore workflow is provisioned here.

Registration/shadow monitoring is on, but callers remain `DISCOVERY_MODE=static`; enabling registry routing, booking, import, or any provider mutation requires a separately reviewed change and operator approval. The staging Postgres volume is persistent across `staging-up` runs; do not treat a rerun as a disposable fresh database, and never delete it to recover a booking ledger. Verify the internal bridge really blocks booking egress on the **actual staging host** before claiming provider isolation; synthetic config cannot prove host firewall or Docker runtime behavior. A separate bot token avoids affecting the production bot, but management cron and the Telegram poller are still live staging processes. If you need *zero* real Telegram/API contact, do not use this deployment; use a fake-provider harness instead.

Do not promote these images to production or count this as the [Step 14 operational validation](service-discovery-rollout-runbook.md). Live Grafana routing, alerts, disruptive drills, legacy inventory, booking keys/restore, and cutover remain owner-authorized and **NOT RUN**. See the [acceptance matrix](service-discovery-acceptance-matrix.md).

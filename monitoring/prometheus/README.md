# Prometheus configuration (production Compose only)

This is a **repository-local deployment template**, not authorization to start or migrate services. Grafana runs separately; alert provisioning and connectivity are covered in later steps. The development Compose file retains its isolated, convenient default network and has no Prometheus service.

## Before any owner-authorized rollout

- Choose a trusted Docker bridge subnet that does not overlap the host, VPN, or other Docker networks. The template uses `172.30.90.0/24`. Create the named bridge **once**, before `docker compose -f docker-compose.prod.yml up`:

  ```sh
  docker network create --driver bridge --subnet 172.30.90.0/24 squash_services
  ```

  Compose declares `squash_services` as external and will **not** create it. If the subnet must change, change the creation command and set `DISCOVERY_ALLOWED_CIDRS` in `.env` to exactly that subnet; the service containers must be on this same network. `DISCOVERY_NETWORK_INTERFACE=eth0` is the single-network Compose default; check the actual interface before registration, or explicitly set `SERVICE_ADVERTISE_HOST` to the container's private address when network layout differs. All containers on this shared bridge, including the database, trust one another: attach only the owner's trusted application/monitoring projects. Network migration of already-running containers requires a planned stop-before-start window; Compose config validation does not perform this migration. Keep management, telegram and booking single-active. Booking retains its 110s shutdown grace.

- Choose one configuration phase below (examples, **not** commands to perform a cutover). Production Compose passes the same `.env` `DISCOVERY_MODE` to management, Telegram and web: a per-caller migration requires an owner-reviewed per-service override or separate projects and review of rendered config, not a global `.env` flip. Follow the [staged rollout handoff](../../docs/service-discovery-rollout-runbook.md). Do not enable booking mutations solely because monitoring is healthy. Set `MONITORED_SERVICE_KEYS` to the required singleton bindings, using the documented defaults in `.env.example` if no override is needed. Management adds its configured booking binding automatically when present.

  | Phase | `.env` values (in addition to the normal application configuration) |
  |---|---|
  | Static only | `DISCOVERY_MODE=static`, no `DISCOVERY_URL`; configured static service URLs remain active. |
  | Shadow registration | `DISCOVERY_MODE=static`, `DISCOVERY_URL=http://management:8080`; keep static URLs active while observing leases/scrapes. |
  | Registry invocation (later, gated) | `DISCOVERY_MODE=registry`, `DISCOVERY_URL=http://management:8080`, `MANAGEMENT_SERVICE_KEY=management`, explicit facility-scoped `BOOKING_SERVICE_KEY`; set `MANAGEMENT_SERVICE_URL=` and `SPORTS_BOOKING_SERVICE_URL=` **empty**. No static fallback exists. |

- Provision a private **absolute host path** for `PROMETHEUS_TOKEN_FILE`, outside the checkout, with a single-line file containing exactly the **same** bearer as the applications' `MONITORING_TOKEN` (not `INTERNAL_API_SECRET`), without a trailing newline. The pinned Prometheus image runs as `nobody` (UID 65534). On a standard non-userns-remapped Linux Docker host, keep the parent directory root-owned and mode `0700`, and make the file owned by UID 65534 with mode `0600` so the container can read it but other host users cannot traverse its parent; for rootless/userns Docker, validate the mapped UID and file readability before rollout. Rotate file and application credentials together. Compose binds the file read-only into Prometheus; it must already exist and be a regular file. Do not commit it, interpolate it into `prometheus.yml`, or give Prometheus the internal API secret. The monitor token authenticates HTTP SD and each application scrape independently. Other production services still consume `MONITORING_TOKEN` via Compose environment variables.

- Prometheus uses pinned `prom/prometheus:v3.9.1` (multi-arch digest in Compose), its own persistent volume, 15-day time retention and a default `4GB` size cap (`PROMETHEUS_RETENTION_SIZE` can be tuned after measurement). Leave free filesystem headroom: TSDB size retention is approximate and WAL/head space is additional. It is excluded from Watchtower. Its query port is bound to `127.0.0.1:9090` on the host (`PROMETHEUS_HOST_PORT` if occupied), **not** directly exposed to remote Grafana. Plan a private/VPN route or an authenticated, allowlisted HTTPS query proxy in a separate owner-authorized step. Do not expose service admin ports or enable Prometheus lifecycle/admin or remote-write receiver endpoints.

## Synthetic checks (no application startup)

From the repo root, with Docker Compose and the Docker daemon available:

```sh
node scripts/checks/monitoring-config-check.mjs
```

This creates a disposable synthetic `--env-file` and dummy bearer file, validates the rendered production Compose topology and runs `promtool check config` using the **same pinned image** with no network. It does not load the owner's `.env`, create the bridge, start the application, or query real services. Full local `make check` does not run this deployment check. The validation does not establish firewall safety, Grafana reachability or alert delivery; those require owner-controlled operational verification.

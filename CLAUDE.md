# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

Four independent binaries in one Go module (`github.com/hutoroff/squash-bot`): `management` (API + scheduler, port 8080), `telegram` (bot, transport configurable — webhook preferred, polling fallback, webhook listener on `SERVER_PORT` default 8083), `booking` (Eversports wrapper, port 8081), `web` (SPA + JWT auth, port 8082).

**API compatibility**: services are compatible within the same major version. The telegram bot calls `GET /version` on the management service at startup and exits if major versions differ.

**Versioning**: each service has `cmd/<service>/VERSION` (format `MAJOR.MINOR.BUILD`), injected at build time via `-ldflags "-X main.Version=<ver>"`. Release workflow is in README.

> [!IMPORTANT]
> - **Never bump a service `VERSION` file as an agent.** Versions are incremented only by CI/CD on GitHub. Leave `cmd/<service>/VERSION` untouched.
> - **Do not write changelogs unless the user explicitly requests one.** Don't create or edit files under `changelogs/` as a side effect of another task.

## Cross-cutting conventions

- **i18n**: Three languages: `en` (default), `de`, `ru`. `i18n.Normalize()` maps any Telegram locale string to one of these. Keys and translations live in `internal/i18n/i18n.go`. Date formatting is locale-aware: English "Sunday, March 22", German "Sonntag, 22. März", Russian "Воскресенье, 22 марта".
- **Message formatting**: `internal/gameformat` produces game announcement text (emoji header, player list, capacity line `courts_count × 2`, "Last updated" footer, "Game completed ✓" marker). Used by `management` (GameNotifier) and `telegram` (formatter.go).
- **Identity model**: `users` is canonical; `user_identities` links one or more providers (currently `telegram` only) to a user; `players` gains a `user_id` and exists only once a user joins a game. All cross-service calls are keyed by `user_id`/`player_id` — never a raw provider ID — resolved once via `POST /api/v1/identities/resolve`. Server-owner is a DB-backed role (`users.is_server_owner`), enforced by `management` itself, not by `web`/`telegram`.
- **Booking lifecycle**: only non-canceled court bookings dated today or later in the group's timezone protect credentials and venues from deletion.
- **Service documentation**: load the relevant skill before planning changes to a service — `/management`, `/telegram`, `/booking`, `/web`.

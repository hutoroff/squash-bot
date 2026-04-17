# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

Four independent binaries in one Go module (`github.com/hutoroff/squash-bot`): `management` (API + scheduler, port 8080), `telegram` (bot, no DB), `booking` (Eversports wrapper, port 8081), `web` (SPA + JWT auth, port 8082).

**API compatibility**: services are compatible within the same major version. The telegram bot calls `GET /version` on the management service at startup and exits if major versions differ.

**Versioning**: each service has `cmd/<service>/VERSION` (format `MAJOR.MINOR.BUILD`), injected at build time via `-ldflags "-X main.Version=<ver>"`. Release workflow is in README.

## Cross-cutting conventions

- **i18n**: Three languages: `en` (default), `de`, `ru`. `i18n.Normalize()` maps any Telegram locale string to one of these. Keys and translations live in `internal/i18n/i18n.go`. Date formatting is locale-aware: English "Sunday, March 22", German "Sonntag, 22. März", Russian "Воскресенье, 22 марта".
- **Message formatting**: `internal/gameformat` produces game announcement text (emoji header, player list, capacity line `courts_count × 2`, "Last updated" footer, "Game completed ✓" marker). Used by `management` (GameNotifier) and `telegram` (formatter.go).
- **Service documentation**: load the relevant skill before planning changes to a service — `/management`, `/telegram`, `/booking`, `/web`.

---
name: documentation
description: Map of where project documentation lives and what it covers. Reminds to keep docs in sync after service changes.
user-invocable: true
---

# Documentation Map

## Where information lives

### README.md
**Audience:** end users, operators, onboarding developers.  
**Always kept current — update when visible behavior changes.**

Covers:
- What the bot does (feature descriptions, bot commands)
- Quick Start and local run instructions
- Venue and game configuration guide (user-facing field descriptions)
- Environment variables for all services (all required/optional vars with descriptions)
- Scheduled tasks — high-level description of each job and its trigger conditions
- Versioning & release workflow (GitHub Actions, image names, GitHub config)
- Production deployment (server setup, updating, backups, health monitoring)
- Project structure overview

### CLAUDE.md
**Audience:** Claude Code only. Loaded in every conversation — keep it short.**  
**Update when cross-cutting code patterns change.**

Covers:
- Binary names, ports, module path
- API compatibility rule (major-version check at startup)
- Version file locations and ldflags pattern
- i18n system (languages, key file location, date formatting)
- `internal/gameformat` usage
- Hint to load service skills before planning changes

### Service skills (`.claude/skills/<service>/SKILL.md`)
**Audience:** Claude Code, loaded on demand.  
**Update when the service's internal structure, interfaces, or behavior changes.**

Each skill covers its service completely:
- Package structure and key files
- Interfaces and type definitions
- DB schema (management skill only)
- HTTP routes and handler details
- Business logic flows and algorithms
- Scheduled job details (management skill only)
- Environment variables (service-specific vars only)
- Constraints and conventions

| Skill | Trigger |
|-------|---------|
| `/management` | Changes to `cmd/management/` |
| `/telegram` | Changes to `cmd/telegram/` |
| `/booking` | Changes to `cmd/booking/` |
| `/web` | Changes to `cmd/web/` |

### Memory files (`.claude/projects/.../memory/`)
**Audience:** Claude Code, loaded selectively across conversations.**  
**Updated automatically during conversations when user preferences or project facts emerge.**

Covers user preferences, project-level decisions, and external references — not code structure.

---

## What to update after a service change

| Change | Update |
|--------|--------|
| New or changed HTTP endpoint | Service SKILL.md routes table |
| New or changed DB table/column | Management SKILL.md DB schema; write migration |
| New or changed scheduled job | Management SKILL.md scheduled job details; README.md Scheduled Tasks |
| New or changed env variable | README.md env vars table; service SKILL.md env section |
| New or changed user-facing feature | README.md (What It Does, Configure Venues, Bot Commands, etc.) |
| New or changed i18n key | Code is the source of truth; no separate doc needed |
| Cross-cutting code convention change | CLAUDE.md |
| New service added | CLAUDE.md architecture section; new SKILL.md; README.md Architecture + Project Structure |

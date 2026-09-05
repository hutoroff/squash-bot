---
name: changelog
description: Use only when the user explicitly requests an application changelog for a management release. Generates a reviewed changelog from git history; never bumps versions or publishes a release.
---

# Requested application changelog

Do not use this as a side effect of another task. Root AGENTS.md still applies: service VERSION files are CI/CD-owned, and publication is not authorized by a changelog request.

## Steps

1. Read `cmd/management/VERSION` (paths in this procedure are relative to the repository root).
2. Ask which version to generate; you may suggest a future minor version, but wait for confirmation and **do not change the VERSION file**.
3. Find the latest management tag:
   ```bash
   git tag --list 'management/v*' --sort=-version:refname | head -1
   ```
4. Read commits since that tag with `git log --oneline <tag>..HEAD`. With no tag, inspect recent history and select relevant commits.
5. Classify `[feat]` as Features and `[fix]` as Fixes. Skip chores, docs, tests, refactors, and merge commits.
6. Strip prefix tags (for example `[feat][web][#14]`) and retain the user-visible change. Combine overlapping entries without inventing behavior.
7. Write `changelogs/<confirmed-version>.md` using the following sections, omitting empty ones:
   ```markdown
   ## Features
   - User-visible feature

   ## Fixes
   - User-visible fix
   ```
8. Show the file for local review. Do not commit, push, release, or trigger an announcement without a separate explicit request.

## Announcement behavior

The filename has no `v` prefix and must match the **running management version** for startup announcement. A future-version file will not announce until that version is deployed; CI/CD controls version increments. Management sends the matching not-yet-announced version to opted-in groups. Bullet text is shown to users, so keep it concise and accurate.

See [the local workflow](../../../docs/agent-workflow.md) and [changelog announcer](../../../cmd/management/service/changelog_announcer.go) for authority and runtime behavior.

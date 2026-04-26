# Changelog Skill

Generate a changelog file for a new management service version from git commit history.

## Steps

1. Read current version from `cmd/management/VERSION`.

2. Ask the user which version to generate the changelog for. Suggest bumping the minor version (e.g. `1.3.0` → `1.4.0`). Wait for confirmation before proceeding.

3. Find the latest git tag matching `management/v*`:
   ```
   git tag --list 'management/v*' --sort=-version:refname | head -1
   ```

4. Get commits since that tag (or all commits if no tag found):
   ```
   git log --oneline <tag>..HEAD
   ```
   If no tag found, use `git log --oneline` and filter to recent relevant commits.

5. Classify commits:
   - Lines containing `[feat]` → **Features** section
   - Lines containing `[fix]` → **Fixes** section
   - Skip: `[chore]`, `[doc]`, `[test]`, `[refactor]`, and merge commits

6. Clean up messages: strip prefix tags like `[feat][web][#14]`, `[fix][#7]` etc. Keep only the human-readable description after the last `]`.

7. Aggregate similar entries: if multiple commits describe the same feature/fix, merge them into a single bullet. Use judgment.

8. Write to `changelogs/<version>.md`:

```markdown
## Features
- <feature description>
- <feature description>

## Fixes
- <fix description>
```

   Omit a section entirely if it has no items.

9. Show the generated file to the user for review. Offer to edit before confirming.

## Notes

- The version in the filename must match the version in `cmd/management/VERSION` exactly for the announcement to fire.
- Do not include a `v` prefix in the filename (e.g. `1.4.0.md`, not `v1.4.0.md`).
- The management service reads this file at startup. If the version has not been announced yet, all groups with `changelog_enabled=true` will receive the message.
- Bullet items are shown verbatim to users in Telegram — keep them concise and user-friendly.

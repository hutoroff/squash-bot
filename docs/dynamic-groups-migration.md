# Dynamic Group Management — Operator Migration Guide

## What changed

Previously the bot only worked with groups listed in `GROUP_CHAT_IDS`.
Migration `004_add_bot_groups` introduces a `bot_groups` database table that
tracks every group the bot is a member of. New groups are registered
automatically when the bot receives a `my_chat_member` Telegram event (i.e.
when it is added to or removed from a group).

`GROUP_CHAT_IDS` is now **optional**. It is read once at startup to seed the
`bot_groups` table and is not used at runtime.

---

## Why you must keep `GROUP_CHAT_IDS` populated for the first upgrade

Telegram **does not** replay `my_chat_member` events for chats the bot is
already in. This means: if you upgrade a running bot and `bot_groups` is empty
while `GROUP_CHAT_IDS` is also empty, the bot will start up with no knowledge
of any group and will not post game announcements anywhere.

The bot logs a clear warning when this condition is detected:

```
WARN No groups registered. Set GROUP_CHAT_IDS to seed existing memberships,
     or remove and re-add the bot to each group to register via Telegram events.
```

---

## Upgrade procedure for already-installed bots

### Step 1 — Keep `GROUP_CHAT_IDS` populated during the upgrade

Ensure your environment still has the variable set to all groups the bot is
currently in:

```
GROUP_CHAT_IDS=-1001234567890,-1009876543210
```

### Step 2 — Deploy and start the new version

On startup the bot will, for each ID in `GROUP_CHAT_IDS`:

1. Call `getChat` to fetch the current group title.
2. Call `getChatMember` to query the bot's **actual** admin status — it never
   assumes `false`; whatever Telegram reports is what gets stored.
3. Upsert the row into `bot_groups`. Running the seed multiple times is safe
   (idempotent `ON CONFLICT DO UPDATE`).

### Step 3 — Verify the table

Connect to your database and confirm the rows look correct:

```sql
SELECT chat_id, title, bot_is_admin, added_at FROM bot_groups;
```

Expected output (one row per group):

```
   chat_id    |    title    | bot_is_admin |          added_at
--------------+-------------+--------------+----------------------------
 -10012345678 | Squash Club |    true      | 2024-06-01 10:00:00+00
```

If `bot_is_admin` is `false` for a group where the bot should be an admin,
check whether it actually has admin rights in that Telegram group.

### Step 4 — Optionally remove `GROUP_CHAT_IDS`

Once `bot_groups` is populated and the bot is running correctly, you may
remove `GROUP_CHAT_IDS` from your environment. The bot will discover and
register any new groups automatically going forward via `my_chat_member`
events.

You may also leave the variable set — re-seeding on every restart is harmless.

---

## New deployment (first install)

For a fresh install you have two options:

**Option A — pre-configure groups**

Set `GROUP_CHAT_IDS` before starting the bot. The groups will be seeded into
`bot_groups` on first startup, and you can remove the variable afterward.

**Option B — start empty, add groups live**

Leave `GROUP_CHAT_IDS` unset. Add the bot to each Telegram group after
startup; each addition triggers a `my_chat_member` event which registers the
group automatically.

---

## Bot permissions and notifications

### When the bot is added to a group

| Bot status after being added | What happens |
|------------------------------|--------------|
| Administrator                | Group registered silently. |
| Member (no admin rights)     | Group registered **and** a DM is sent to the person who added the bot explaining that admin rights are required to pin announcements. |

### When the bot is demoted from administrator to member

The person who changed the permissions receives a DM:

> I've lost administrator permissions in "Squash Club".
> Without admin rights I can no longer pin game announcements.

### When the bot is removed from a group

The group row is deleted from `bot_groups`. No notification is sent.

### What admin rights are needed for

| Feature                        | Requires admin? |
|--------------------------------|-----------------|
| Post game announcements        | No              |
| Handle "I'm in" / "I'll skip"  | No              |
| Pin the announcement message   | **Yes**         |
| Unpin after the game           | **Yes**         |

The bot functions without admin rights but announcements will not be pinned.

---

## Environment variable reference

```bash
# Comma-separated list of group chat IDs.
# Required during the first upgrade from a version without bot_groups.
# Optional afterward — new groups are registered via Telegram events.
GROUP_CHAT_IDS=-1001234567890,-1009876543210
```

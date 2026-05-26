---
name: telegram
description: Architecture reference for the squash-bot telegram service (cmd/telegram). Load before planning any changes to bot handlers, callback routing, wizard state machines, or the management client.
user-invocable: true
---

# Telegram Service — Architecture Reference

The telegram bot handles all user-facing Telegram interactions. It has no database access — all data operations go through HTTP calls to the management service via `client.ManagementClient`. All wizard state is in-memory using `sync.Map`.

**Entry point:** `cmd/telegram/main.go`  
**Transport:** Telegram long-polling (no HTTP server port)  
**Module path:** `github.com/hutoroff/squash-bot/cmd/telegram`

---

## Package structure

```
cmd/telegram/
├── main.go                  — wiring: BotAPI, ManagementClient, Bot construction
├── telegram/
│   ├── bot.go               — Bot struct definition, New(), Start(), processUpdate()
│   ├── handlers.go          — handleMessage, handleCallback dispatcher, reply/answerCallback helpers,
│   │                          normalizeCourts, parseAdminCommand, isBotMentioned, isKnownGroupMention
│   ├── callback_router.go   — buildCallbackRouter(): map[string]callbackHandler (35+ entries)
│   ├── commands.go          — handleCommand switch, /newGame, /language, /trigger, /games, /myGame
│   ├── formatter.go         — formatGamesListMessage, superGroupMessageLink
│   ├── participation_handlers.go — handleJoin, handleSkip, handleGuestAdd, handleGuestRemove
│   ├── game_manage_handlers.go  — handleManage, handleManageShowPlayers/Guests, handleManageEditCourts,
│   │                              handleManageClose, handleManageKickPlayer/Guest,
│   │                              handleManageCourtsToggle, handleManageCourtsConfirm,
│   │                              handleManageCourtsCancelConfirm, handleManageCourtsCancelAbort,
│   │                              handleManagePublish (Publish button for unpublished games),
│   │                              handleManageCourtsMode, renderCourtsModePicker,
│   │                              handleManageBook, renderBookCountKeyboard,
│   │                              handleManageBookCancel,
│   │                              courtsBeingRemoved, renderCourtCancelPrompt, processCourtsEdit
│   ├── newgame_handlers.go  — /newGame wizard: handleNewGameDate/Group/Venue/CourtToggle/
│   │                          CourtConfirm/TimeSlot/TimeCustom, processNewGameWizard,
│   │                          buildDateSelectionKeyboard, renderCourtPickKeyboard, renderTimeSlotKeyboard
│   ├── settings_handlers.go — handleTrigger, handleSetLangGroup, handleSetLang,
│   │                          handleSetTzPick, handleSetTz, handleToggleChangelog,
│   │                          handleNewGameGroupVenue, handleGroupSelection
│   │                          renderLanguageKeyboard(ctx, groupID) — fetches group to show changelog toggle label
│   └── venue_handlers.go    — handleVenueList/Add/EditMenu/StartEdit/Delete/DeleteConfirm,
│                              handleVenueDayToggle/Confirm, handleVenueWizPreferredTimePick,
│                              handleVenuePtimeToggle, handleVenuePtimeConfirm, handleVenuePtimeSet,
│                              renderPreferredTimeEditKeyboard, joinSelectedTimesOrdered,
│                              processVenueWizard, processVenueEdit
└── client/
    ├── interface.go         — ManagementClient interface (39 methods across 5 groups)
    └── client.go            — *Client HTTP implementation (satisfies ManagementClient structurally)
```

---

## Bot struct (`bot.go`)

```go
type Bot struct {
    api                           *tgbotapi.BotAPI
    client                        client.ManagementClient   // interface, not *client.Client
    serviceAdminIDs               map[int64]bool
    loc                           *time.Location
    logger                        *slog.Logger
    // In-memory wizard state — all sync.Map, keyed by private chatID int64
    pendingGames                  sync.Map  // pendingGameKey → *pendingGame
    pendingCourtsEdit             sync.Map  // chatID → gameID int64
    pendingManageCourtsToggle     sync.Map  // chatID → *manageCourtsToggleState
    pendingNewGameWizard          sync.Map  // chatID → *newGameWizard
    pendingVenueWizard            sync.Map  // chatID → *venueWizard
    pendingVenueEdit              sync.Map  // chatID → *venueEditState
    pendingVenueGameDaysEdit      sync.Map  // chatID → *venueGameDaysEditState
    pendingVenuePreferredTimeEdit sync.Map  // chatID → *venuePreferredTimeEditState
    pendingGroupVenuePick         sync.Map  // chatID → *groupVenuePickState
    pendingVenueCredAdd                sync.Map  // chatID → *venueCredWizard
    pendingManageCourtsCancelPrompt    sync.Map  // chatID → *manageCourtsCancelPromptState
    pendingManageBookCount             sync.Map  // chatID → *manageBookCountState
    handlerSem                         chan struct{}  // semaphore, maxConcurrentHandlers=50
    callbackRouter                map[string]callbackHandler
}
```

`New()` signature: `New(api *tgbotapi.BotAPI, loc *time.Location, mgmtClient client.ManagementClient, serviceAdminIDs string, logger *slog.Logger) *Bot`

---

## Update processing flow

```
Start() long-poll loop
  → processUpdate()
      message → handleMessage()
          private + slash command → clear all pending state, handleCommand()
          private + active wizard → route to active wizard processor
          group @mention         → /help|/start only; others → redirect to private
      callback_query → handleCallback()
          split "action:rawID" on first colon
          look up action in callbackRouter map
          call handler(ctx, cb, rawID)
      my_chat_member → handleMyChatMember()
          left/kicked    → RemoveGroup
          member/admin   → UpsertGroup + optional DM notification
```

**Concurrency:** Every update runs in a goroutine. `handlerSem` (buffered channel, cap=50) prevents more than 50 concurrent handlers. Updates block (with context cancellation) waiting for a slot — they are NOT dropped.

---

## Callback routing (`callback_router.go`)

Callback data format: `"action:rawID"` — always exactly one colon split.

`buildCallbackRouter()` returns a `map[string]callbackHandler` built once at Bot construction. Handler type: `func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string)`.

Local helpers inside the builder:
- `int64H(fn)` — wraps handlers expecting a parsed int64 payload
- `splitTwo(rawID, data)` — splits on first colon → `(p1, p2, ok)`
- `parseVenueGroup(rawID, data)` — parses `venueID:groupID` → two int64

**Adding a new callback action:** Add an entry to the map in `callback_router.go`. Do not modify `handleCallback()` in `handlers.go`.

All callback actions:
```
join, skip, guest_add, guest_remove
manage, manage_players, manage_guests, manage_courts, manage_close
manage_kick, manage_kick_guest, manage_court_toggle, manage_court_confirm
manage_courts_cancel_confirm (confirm cancel-booking-and-remove after pre-flight prompt)
manage_courts_cancel_abort   (go back to court toggle keyboard, restoring prior selection)
manage_courts_mode:<gameID>:auto|manual  (mode picker: choose auto-book vs manual edit)
manage_book:<gameID>:<count>             (book N courts via BookGameCourts)
manage_book_cancel:<gameID>              (dismiss book-count picker, re-render manage screen)
publish_game (sends unpublished game to group via POST /api/v1/games/{id}/publish)
select_group (3-part: originChatID:originMsgID:groupID)
ng_date, ng_group, ng_venue, ng_court_toggle, ng_court_confirm
ng_timeslot, ng_time_custom, ng_gvenue
trigger, set_lang_group, set_lang, set_tz_pick, set_tz, toggle_changelog
venue_list, venue_add, venue_edit, venue_edit_name, venue_edit_courts
venue_edit_slots, venue_edit_addr, venue_edit_gamedays, venue_edit_graceperiod
venue_edit_preferred_time, venue_edit_auto_booking_courts, venue_edit_booking_opens_days
venue_delete, venue_delete_ok, venue_day_toggle, venue_day_confirm
venue_wiz_ptime, venue_ptime_toggle, venue_ptime_confirm, venue_ptime_set
```

---

## Wizard state machines

### New Game Wizard (`newgame_handlers.go`)
State: `pendingNewGameWizard sync.Map` (chatID → `*newGameWizard`)

```go
type newGameWizard struct {
    groupID        int64           // set for multi-group admins
    gameDate       time.Time
    dateStr        string          // raw YYYY-MM-DD for re-parsing in group timezone
    loc            *time.Location  // group timezone
    step           wizardStep      // Group → Venue → CourtPick → Time → Courts
    venueID        *int64
    venueCourts    []string
    selectedCourts map[string]bool
    timeSlots       []string
    preferredGameTimes string
}
```

Steps: `wizardStepGroup` → `wizardStepVenue` → `wizardStepCourtPick` → `wizardStepTime` → `wizardStepCourts`

Callbacks: `ng_date`, `ng_group`, `ng_venue`, `ng_court_toggle`, `ng_court_confirm`, `ng_timeslot`, `ng_time_custom`, `ng_gvenue`

Any slash command in private chat clears `pendingNewGameWizard` (and all other pending state).

### Venue Creation Wizard (`venue_handlers.go`)
State: `pendingVenueWizard sync.Map` (chatID → `*venueWizard`)

Steps: `venueStepName` → `venueStepCourts` → `venueStepTimeSlots` → `venueStepPreferredTime` → `venueStepAddress` → `venueStepGameDays` → `venueStepGracePeriod` → [if `autoBookingAllowed`] `venueStepAutoBookingEnabled` → `venueStepAutoBookingCourts` → `venueStepBookingOpensDays` [else → `venueStepBookingOpensDays`]

The wizard fetches `group.AutoBookingAllowed` via `GetGroupByID` at start and stores it on the `venueWizard` struct. When `autoBookingAllowed` is false, auto-booking steps are skipped entirely and `autoBookingEnabled` stays false.

### Venue Edit (`venue_handlers.go`)
State: `pendingVenueEdit sync.Map` (chatID → `*venueEditState`)

```go
type venueEditState struct {
    venueID int64
    groupID int64
    field   venueEditField  // name/courts/slots/address/gameDays/gracePeriod/preferredTime/autoBookingCourts/bookingOpensDays
}
```

Game-days uses a separate toggle state: `pendingVenueGameDaysEdit` (chatID → `*venueGameDaysEditState`)
Preferred-times uses: `pendingVenuePreferredTimeEdit` (chatID → `*venuePreferredTimeEditState{venueID, groupID int64, selectedTimes map[string]bool}`) — toggle+confirm pattern identical to game days; `venue_ptime_toggle:<slot>` toggles a slot, `venue_ptime_confirm:_` joins selected times ordered by time_slots and submits.

### Venue Credential Wizard (`venue_handlers.go`)
State: `pendingVenueCredAdd sync.Map` (chatID → `*venueCredWizard`)

Steps: `venueCredStepLogin` → `venueCredStepPriority` → `venueCredStepMaxCourts` (integer or `-` for default 3) → `venueCredStepPassword` (password message deleted immediately before any API call)

`venueCredWizard` struct: `venueID, groupID int64`, `login string`, `priority int`, `maxCourts int` (0 = use default 3), `step venueCredStep`, `suggested int`, `inUse []int`

The "🔑 Credentials" button in the venue edit menu is only shown when `venue.AutoBookingEnabled = true`. Credentials button requires `CREDENTIALS_ENCRYPTION_KEY` on the management service; omitting it returns 503. Callbacks: `venue_creds:{venueID}:{groupID}`, `venue_cred_add:{venueID}:{groupID}`, `venue_cred_del:{credID}:{venueID}:{groupID}`, `venue_cred_del_ok:{credID}:{venueID}:{groupID}`.

### Courts Edit (`game_manage_handlers.go`)
State: `pendingManageCourtsToggle sync.Map` (chatID → `*manageCourtsToggleState`)

### Courts Cancel Prompt (`game_manage_handlers.go`)
State: `pendingManageCourtsCancelPrompt sync.Map` (chatID → `*manageCourtsCancelPromptState`)

```go
type manageCourtsCancelPromptState struct {
    gameID       int64
    groupID      int64
    newCourts    string    // the confirmed court selection being applied
    bookedLabels []string  // court labels that have active bookings
    venueCourts  []string  // all venue courts (for restoring toggle keyboard on abort)
}
```

Populated by `handleManageCourtsConfirm` when `ListActiveCourtBookings` returns active bookings for removed courts. Cleared by both confirm and abort handlers.

---

## Business logic flows

### Language & Timezone Selection (`/language`)

1. Admin sends `/language` in private chat.
2. If admin in exactly one group → show language picker for that group immediately.
3. If admin in multiple groups → show group picker first (`set_lang_group:<groupID>` callbacks), then language picker.
4. Language picker: 3 language buttons + "🕐 Set Timezone" button + changelog toggle button.
5. Language button → `set_lang:<lang>:<groupID>` → `PATCH /api/v1/groups/{chatID}/language`.
6. "Set Timezone" → `set_tz_pick:<groupID>` → curated timezone picker (18 IANA timezones, 2 per row).
7. Timezone button → `set_tz:<groupID>:<tz>` → `PATCH /api/v1/groups/{chatID}/timezone`.
8. Changelog toggle → `toggle_changelog:<groupID>` → `SetGroupChangelog` → `PATCH /api/v1/groups/{chatID}/changelog`.
   Button label shows current state ("📢 Changelog: ON" / "📢 Changelog: OFF"); `renderLanguageKeyboard` fetches group to determine label.
9. Management returns 400 for invalid IANA strings, 404 if group not found.

### Venue Management (`/venues`)

Works in private chat only.

1. Admin sends `/venues` → venue list for their group (or group picker if multiple groups).
2. Each venue row: "Edit" and "Delete" buttons; "Add Venue" at bottom.
3. **Add venue wizard**: name → courts (comma-separated) → time slots (HH:MM, `-` to skip) → preferred game times (toggle keyboard: tap slots to toggle ✓ + "✓ Done" confirm / "✕ No preference" skip; skipped if no time slots entered) → address (`-` to skip) → game days (toggle keyboard: tap days + "✓ Confirm") → grace period (int or `-` for default 24) → auto-booking enabled ("Enable"/"Disable" inline) → auto-booking courts (ordered subset or `-`; only shown when enabled) → booking opens days (int or `-` for default 14) → venue created.
4. **Edit venue**: opens edit menu with current values. Free-text fields (Name, Courts, Time Slots, Address, Grace Period, Auto-booking Courts, Booking Opens Days): admin sends new value as a message. Inline-keyboard fields: Game Days (toggle + Confirm), Preferred Times (toggle keyboard pre-seeded from current `preferred_game_times` + "✓ Done" / "✕ Clear"). When `auto_booking_enabled = true`, a "🔑 Credentials" button also appears. When the group's `auto_booking_allowed` is false, the edit menu suppresses the auto-booking status line, toggle button, courts/count buttons, and credentials button.
5. **Delete venue**: two-step confirmation. Blocked with user-friendly message if venue has active `court_bookings` (HTTP 409 → `MsgVenueHasActiveBookings`). Linked games keep `venue_id` as NULL (ON DELETE SET NULL in DB).
6. **Credential management**: "🔑 Credentials" lists stored credentials (masked login, priority, max_courts) with "Add" / "Delete". Only shown when `auto_booking_enabled = true`. Requires `CREDENTIALS_ENCRYPTION_KEY` on management service (else 503). Add-credential wizard: login → priority (current values shown) → max courts (int or `-` for default 3) → password (message deleted immediately before any API call). Deletion is two-step; blocked with user-friendly message if credential has active court bookings (HTTP 409 → `MsgVenueCredHasActiveBookings`).

**Venue field semantics:**
- `grace_period_hours`: hours before game when cancellation reminder fires (default 24). Reminder at `game_date − (grace_period_hours + 6)h`.
- `game_days`: comma-separated Go `time.Weekday` ints (Sun=0 … Sat=6). Drives booking reminder schedule.
- `booking_opens_days`: days ahead when booking opens (default 14). Shown in booking reminder DM.
- `preferred_game_times`: comma-separated HH:MM slots (each must be one of `time_slots`, or empty for no preference). Multiple slots drive N auto-bookings and N games per day. All matching slots shown with ⭐ in new-game time picker. Edited via toggle+confirm keyboard (same pattern as game days).
- `auto_booking_enabled`: enables AutoBookingJob for this venue (default false). Toggled via `venue_toggle_autobooking:<venueID>:<groupID>` or during creation wizard.
- `auto_booking_courts`: ordered comma-separated court numbers (subset of `courts`). AutoBookingJob books in declared priority order; cancellation reminder cancels in **reverse** priority order (lowest first). Court matching uses name-extracted number (`extractCourtNumber("Court 7")→7`), not Eversports numeric IDs.

### New Game Wizard (`/newGame`)

Works in private chat only. Group @mentions redirected to private chat. At least one venue must exist per group.

**Single-group admin:**
1. `/newGame` → date-picker keyboard (today + next 13 days).
2. Tap date (`ng_date:<YYYY-MM-DD>`) → if 1 venue: auto-select + court toggle; if 2+: venue picker.
3. Select venue (`ng_venue:<venueID>`) → court toggle keyboard (✓ when selected) + Confirm.
4. Toggle courts (`ng_court_toggle:<court>`), confirm (`ng_court_confirm:_`) → time slot buttons + "Custom time".
5. Select slot (`ng_timeslot:<HH:MM>`) or "Custom time" (`ng_time_custom:_`, reverts to free-text) → game created.

**Multi-group admin:**
1. `/newGame` → date-picker keyboard.
2. Tap date → group picker (`ng_group:<groupID>` buttons).
3. Select group → venues fetched; if 0: error; if 1: auto-select + court toggle; if 2+: venue picker.
4–5. Same as single-group steps 3–5.

### Courts Update (`/games` → Manage → Edit Courts)

**Mode picker (auto-booking venues):**

When `handleManageEditCourts` fires for a game whose venue has `AutoBookingEnabled = true`, it calls `GetVenueBookingReadiness` first:
- If `readiness.Ready && readiness.MaxCourts > 0` → stores `manageBookCountState{gameID, groupID, max}` in `pendingManageBookCount`, calls `renderCourtsModePicker` which shows two buttons:
  - `BtnEditCourtsAutoBook` → `manage_courts_mode:<gameID>:auto`
  - `BtnEditCourtsManual`   → `manage_courts_mode:<gameID>:manual`
- On readiness error or `!ready` → falls through to the normal toggle keyboard (admin is never blocked).

`handleManageCourtsMode` (rawID = `<gameID>:<mode>`):
- `"auto"` → validates state, calls `renderBookCountKeyboard` (buttons 1..max, each `manage_book:<gameID>:<n>` + Cancel button `manage_book_cancel:<gameID>`).
- `"manual"` → clears `pendingManageBookCount`, shows the normal toggle keyboard or free-text.

`handleManageBook` (rawID = `<gameID>:<count>`): validates admin, calls `client.BookGameCourts`, shows:
- All booked → `MsgBookSuccess` (toast + refreshes manage screen)
- Some booked → `MsgBookPartial`
- None booked → `MsgBookNoneBooked`

`handleManageBookCancel` (rawID = `<gameID>`): toast `MsgBookCanceled` + re-renders manage screen.

`manageBookCountState` struct: `gameID, groupID, max int64/int`.

**Normal toggle flow (manual mode or venues without auto-booking):**

- If game has a venue with courts configured → inline court-toggle keyboard (same ✓ UX). Pre-selects current courts. Confirm → `manage_court_confirm:<gameID>`.
- If game has no venue → falls back to free-text input.

**Booking cancellation pre-flight (two-step confirmation):**

When admin confirms court removal (`manage_court_confirm`), the handler:
1. Computes which courts are being removed via `courtsBeingRemoved` (multiset diff).
2. Calls `client.ListActiveCourtBookings(gameID, removedCourts)` to check for active Eversports bookings.
3. On error → answers callback, shows `MsgSomethingWentWrong`, returns (blocking — no silent fallthrough).
4. On active bookings found → stores `*manageCourtsCancelPromptState` in `pendingManageCourtsCancelPrompt`, answers callback, calls `renderCourtCancelPrompt` which edits the message with:
   - Text: `MsgCourtCancelPromptSingle` / `MsgCourtCancelPromptMulti` (lists booked court labels)
   - Buttons: `BtnCourtCancelConfirm` (`manage_courts_cancel_confirm:<gameID>`) + `BtnCourtCancelAbort` (`manage_courts_cancel_abort:<gameID>`)
5. On no active bookings → proceeds directly to `UpdateCourts` as before (no prompt).

`handleManageCourtsCancelConfirm`: re-validates admin via `isAdminInGroup`, clears both pending states, calls `UpdateCourtsAndCancelBookings`. Partial failures → `MsgCourtCancelPartial`; full success → `MsgCourtCancelSuccess`.

`handleManageCourtsCancelAbort`: clears cancel prompt state, restores `pendingManageCourtsToggle` pre-seeded from `state.newCourts`, calls `renderManageCourtsKeyboard`.

### Button Click Flow (join / skip / guest)

1. Parse callback data (`action:game_id`, e.g. `join:123`).
2. Call management service (upsert participation or add/remove guest).
3. `answerCallback` immediately with success or error — user sees real result with no delay.
4. Call `scheduleGameMessageEdit(gameID)` to enqueue an async re-render.

**Coalesced message editing (`gameEditWorker` in `bot.go`, methods in `participation_handlers.go`):**
- `editWorkers sync.Map` (keyed by `gameID`) holds one `*gameEditWorker` per game.
- `schedule(run)`: if a worker goroutine is already running, sets `pending=true` and returns; otherwise starts a goroutine that loops: `run()` → check pending → repeat or exit.
- This collapses N concurrent button clicks into at most 2 sequential `EditMessageText` calls.
- `doEditGameMessage` fetches fresh state on each attempt and handles HTTP 429 with a `RetryAfter`-aware sleep + single retry; silently drops `400 "message is not modified"` errors.

### Admin & Group Management

- **Group admin rights**: verified per-action via `GetChatAdministrators` — no hardcoded IDs. Controls game creation, player/guest management, all `/games` actions.
- **Service admins** (`SERVICE_ADMIN_IDS`): Telegram user IDs with `/trigger` access only. Completely independent of group membership or Telegram admin status.
- `my_chat_member` events: bot added without admin rights → DM the adder; added with rights / promoted / demoted / removed → `UpsertGroup` or `RemoveGroup`.

### Guest Management

- Players add guests (+1) linked to their own player record.
- Players remove their own most-recently-added guest.
- Admins remove any guest via the `/games` management menu (`manage_kick_guest` callback).

---

## ManagementClient interface (`client/interface.go`)

44 methods across 6 groups. `*client.Client` satisfies this structurally — no explicit declaration.

```
Games:          CreateGame, GetGameByID, UpdateMessageID, UpdateCourts,
                GetUpcomingGamesByChatIDs, GetNextGameForTelegramUser,
                PublishGame(ctx, gameID, actorTgID int64, actorDisplay string) (*models.Game, error),
                ListActiveCourtBookings(ctx, gameID int64, courts []string) ([]CourtBookingInfo, error),
                UpdateCourtsAndCancelBookings(ctx, gameID, groupID int64, newCourts, actorDisplay string, actorTgID int64) (canceledLabels []string, failed []CancelFailure, err error),
                BookGameCourts(ctx, gameID, groupID, actorTgID int64, actorDisplay string, count int) (*BookGameCourtsResult, error)
Participations: Join, Skip, AddGuest, RemoveGuest, GetParticipations, GetGuests,
                KickPlayer, KickGuestByID
Groups:         UpsertGroup, RemoveGroup, GetGroups, GroupExists, GetGroupByID,
                SetGroupLanguage, SetGroupTimezone, SetGroupChangelog, SetGroupAutoBookingAllowed
Venues:         CreateVenue, GetVenuesByGroup, GetVenueByID, UpdateVenue, DeleteVenue,
                GetVenueBookingReadiness(ctx, venueID, groupID int64) (*BookingReadiness, error)
VenueCredentials: AddVenueCredential(ctx, venueID, groupID, login, password, priority, maxCourts),
                  ListVenueCredentials, DeleteVenueCredential, ListVenueCredentialPriorities
Scheduler:      TriggerScheduledEvent
```

**Client-side types** (defined in `client.go`, used by handlers):
- `CourtBookingInfo{CourtLabel, GameTime, MatchID string}` — returned by `ListActiveCourtBookings`
- `CancelFailure{Court, Reason string}` — element of `failed` slice returned by `UpdateCourtsAndCancelBookings` on partial failure; mapped from HTTP 200+JSON body `{canceled:[], failed:[{court,reason}]}`
- `BookGameCourtsResult{Requested, BookedCount int, BookedLabels []string, Failures []BookingCourtsFailure}` — returned by `BookGameCourts`; HTTP 409 → `ErrAutoBookingNotAvailable`
- `BookingReadiness{Ready bool, MaxCourts int, Reason string}` — returned by `GetVenueBookingReadiness`

**Error propagation:** `client.go` defines `HTTPError{StatusCode int, Message string}` — a typed error returned by `parseErrorBody`. Handlers use `errors.As(err, &httpErr)` to branch on specific HTTP status codes (e.g. 409 Conflict) before falling through to generic error messages. Always return `*HTTPError` from `parseErrorBody` for new error cases; do not wrap with `fmt.Errorf`.

`client.go` also defines package-level sentinel errors for specific status codes: `ErrAlreadyPublished` (mapped from HTTP 409 on `PublishGame`), `ErrAutoBookingNotAvailable` (mapped from HTTP 409 on `BookGameCourts`). Handlers use `errors.Is` to show dedicated messages.

**Adding a new management API call:** Add the method to `ManagementClient` in `client/interface.go`, implement it in `client/client.go`, then use it in the appropriate handler file.

---

## Language resolution

- **Group messages** (game announcements, callback responses that edit group messages): use `b.groupLocalizer(ctx, chatID)` which calls `GetGroupByID` and reads `group.Language`
- **Private messages** (DMs, wizard interactions): use `b.userLocalizer(msg.From.LanguageCode)` which reads the Telegram user's `LanguageCode` field
- Never use `userLocalizer` for group-visible text; never use `groupLocalizer` for private DMs

---

## Environment variables

```
TELEGRAM_BOT_TOKEN=           required (bot token from @BotFather)
MANAGEMENT_SERVICE_URL=       required (e.g. http://management:8080)
INTERNAL_API_SECRET=          required (must match management service value)
LOG_LEVEL=INFO
LOG_DIR=                      optional; writes $LOG_DIR/app.log (10 MB / 5 backups, gzip) + stdout
TIMEZONE=UTC
SERVICE_ADMIN_IDS=            optional; comma-sep Telegram user IDs allowed to use /trigger
```

---

## Conventions and constraints

- Telegram message IDs are scoped per-chat — `pendingGameKey{chatID, messageID}` prevents collisions
- Callback data format is stable — changing it requires coordinated migration (old messages in Telegram still have old button data)
- UTF-16 encoding matters for `@mention` entity offsets — see `isBotMentioned` in `handlers.go`
- `b.client` field is `client.ManagementClient` (interface), never `*client.Client` — this enables test doubles
- `/games` admin list prefixes unpublished games (those with `game.MessageID == nil`) with `📝` via `formatGamesListMessage` in `commands.go`; the Manage screen for such games shows a "📢 Publish" button as the first row via `renderManageScreen` in `game_manage_handlers.go`
- `serviceAdminIDs` (env `SERVICE_ADMIN_IDS`) controls `/trigger` only — group admin rights are verified via `GetChatAdministrators` at runtime
- Updates can arrive concurrently for the same user — wizard state in `sync.Map` is safe, but individual wizard steps within a single chat are processed sequentially because only one update per user tends to be in flight

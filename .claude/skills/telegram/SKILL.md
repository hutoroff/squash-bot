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
│   │                              processCourtsEdit
│   ├── newgame_handlers.go  — /newGame wizard: handleNewGameDate/Group/Venue/CourtToggle/
│   │                          CourtConfirm/TimeSlot/TimeCustom, processNewGameWizard,
│   │                          buildDateSelectionKeyboard, renderCourtPickKeyboard, renderTimeSlotKeyboard
│   ├── settings_handlers.go — handleTrigger, handleSetLangGroup, handleSetLang,
│   │                          handleSetTzPick, handleSetTz, handleNewGameGroupVenue,
│   │                          handleGroupSelection
│   └── venue_handlers.go    — handleVenueList/Add/EditMenu/StartEdit/Delete/DeleteConfirm,
│                              handleVenueDayToggle/Confirm, handleVenueWizPreferredTimePick,
│                              handleVenuePtimeSet, processVenueWizard, processVenueEdit
└── client/
    ├── interface.go         — ManagementClient interface (37 methods across 5 groups)
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
    pendingVenueCredAdd           sync.Map  // chatID → *venueCredWizard
    handlerSem                    chan struct{}  // semaphore, maxConcurrentHandlers=50
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
select_group (3-part: originChatID:originMsgID:groupID)
ng_date, ng_group, ng_venue, ng_court_toggle, ng_court_confirm
ng_timeslot, ng_time_custom, ng_gvenue
trigger, set_lang_group, set_lang, set_tz_pick, set_tz
venue_list, venue_add, venue_edit, venue_edit_name, venue_edit_courts
venue_edit_slots, venue_edit_addr, venue_edit_gamedays, venue_edit_graceperiod
venue_edit_preferred_time, venue_edit_auto_booking_courts, venue_edit_booking_opens_days
venue_delete, venue_delete_ok, venue_day_toggle, venue_day_confirm
venue_wiz_ptime, venue_ptime_set
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
    timeSlots      []string
    preferredGameTime string
}
```

Steps: `wizardStepGroup` → `wizardStepVenue` → `wizardStepCourtPick` → `wizardStepTime` → `wizardStepCourts`

Callbacks: `ng_date`, `ng_group`, `ng_venue`, `ng_court_toggle`, `ng_court_confirm`, `ng_timeslot`, `ng_time_custom`, `ng_gvenue`

Any slash command in private chat clears `pendingNewGameWizard` (and all other pending state).

### Venue Creation Wizard (`venue_handlers.go`)
State: `pendingVenueWizard sync.Map` (chatID → `*venueWizard`)

Steps: `venueStepName` → `venueStepCourts` → `venueStepTimeSlots` → `venueStepPreferredTime` → `venueStepAddress` → `venueStepGameDays` → `venueStepGracePeriod` → `venueStepAutoBookingCourts` → `venueStepBookingOpensDays`

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
Preferred-time uses: `pendingVenuePreferredTimeEdit` (chatID → `*venuePreferredTimeEditState`)

### Venue Credential Wizard (`venue_handlers.go`)
State: `pendingVenueCredAdd sync.Map` (chatID → `*venueCredWizard`)

Steps: `venueCredStepLogin` → `venueCredStepPriority` → `venueCredStepPassword` (password message deleted immediately before any API call)

The "🔑 Credentials" button in the venue edit menu is only shown when `venue.AutoBookingEnabled = true`. Credentials button requires `CREDENTIALS_ENCRYPTION_KEY` on the management service; omitting it returns 503. Callbacks: `venue_creds:{venueID}:{groupID}`, `venue_cred_add:{venueID}:{groupID}`, `venue_cred_del:{credID}:{venueID}:{groupID}`, `venue_cred_del_ok:{credID}:{venueID}:{groupID}`.

### Courts Edit (`game_manage_handlers.go`)
State: `pendingManageCourtsToggle sync.Map` (chatID → `*manageCourtsToggleState`)

---

## ManagementClient interface (`client/interface.go`)

41 methods across 6 groups. `*client.Client` satisfies this structurally — no explicit declaration.

```
Games:          CreateGame, GetGameByID, UpdateMessageID, UpdateCourts,
                GetUpcomingGamesByChatIDs, GetNextGameForTelegramUser
Participations: Join, Skip, AddGuest, RemoveGuest, GetParticipations, GetGuests,
                KickPlayer, KickGuestByID
Groups:         UpsertGroup, RemoveGroup, GetGroups, GroupExists, GetGroupByID,
                SetGroupLanguage, SetGroupTimezone
Venues:         CreateVenue, GetVenuesByGroup, GetVenueByID, UpdateVenue, DeleteVenue
VenueCredentials: AddVenueCredential, ListVenueCredentials, DeleteVenueCredential,
                  ListVenueCredentialPriorities
Scheduler:      TriggerScheduledEvent
```

**Adding a new management API call:** Add the method to `ManagementClient` in `client/interface.go`, implement it in `client/client.go`, then use it in the appropriate handler file.

---

## Language resolution

- **Group messages** (game announcements, callback responses that edit group messages): use `b.groupLocalizer(ctx, chatID)` which calls `GetGroupByID` and reads `group.Language`
- **Private messages** (DMs, wizard interactions): use `b.userLocalizer(msg.From.LanguageCode)` which reads the Telegram user's `LanguageCode` field
- Never use `userLocalizer` for group-visible text; never use `groupLocalizer` for private DMs

---

## Conventions and constraints

- Telegram message IDs are scoped per-chat — `pendingGameKey{chatID, messageID}` prevents collisions
- Callback data format is stable — changing it requires coordinated migration (old messages in Telegram still have old button data)
- UTF-16 encoding matters for `@mention` entity offsets — see `isBotMentioned` in `handlers.go`
- `b.client` field is `client.ManagementClient` (interface), never `*client.Client` — this enables test doubles
- `serviceAdminIDs` (env `SERVICE_ADMIN_IDS`) controls `/trigger` only — group admin rights are verified via `GetChatAdministrators` at runtime
- Updates can arrive concurrently for the same user — wizard state in `sync.Map` is safe, but individual wizard steps within a single chat are processed sequentially because only one update per user tends to be in flight

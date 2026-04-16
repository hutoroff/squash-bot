package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

// pendingGameKey uniquely identifies a pending group-selection request.
// Telegram message IDs are scoped per-chat, so using messageID alone as a key
// allows two admins in different private chats to collide on the same ID.
// Including chatID makes the pair globally unique.
type pendingGameKey struct {
	chatID    int64
	messageID int
}

// pendingGame holds the parsed game details while the admin selects a target group.
type pendingGame struct {
	gameDate    time.Time
	courts      string
	venueID     *int64
	replyChatID int64
	replyMsgID  int
}

// wizardStep tracks which input the /newGame wizard is waiting for.
type wizardStep int

const (
	wizardStepGroup     wizardStep = iota // waiting for group selection (multi-group admin)
	wizardStepVenue                       // waiting for venue selection (button)
	wizardStepCourtPick                   // waiting for court toggle + confirm (buttons)
	wizardStepTime                        // waiting for time text input or slot button
	wizardStepCourts                      // waiting for courts text input (no-venue path)
)

// newGameWizard holds the in-progress state for the /newGame wizard.
// Keyed by private chat ID in pendingNewGameWizard.
type newGameWizard struct {
	groupID           int64          // set for multi-group admins after group is selected
	gameDate          time.Time      // date only (midnight) at venue/time step; full datetime at courts step
	dateStr           string         // raw "YYYY-MM-DD" from the date picker; used to re-parse in group timezone
	loc               *time.Location // group timezone; set once the group is known, falls back to bot loc
	step              wizardStep
	venueID           *int64          // set when a venue is selected
	venueCourts       []string        // available courts from the selected venue
	selectedCourts    map[string]bool // toggle state for court picker
	timeSlots         []string        // available time slots from the selected venue
	preferredGameTime string          // venue's preferred game time; highlighted in the time slot keyboard
}

// venueWizardStep tracks which field the venue creation wizard is collecting.
type venueWizardStep int

const (
	venueStepName venueWizardStep = iota
	venueStepCourts
	venueStepTimeSlots
	venueStepPreferredTime // only shown when time_slots are set
	venueStepAddress
	venueStepGameDays
	venueStepGracePeriod
	venueStepAutoBookingEnabled // enable/disable automatic booking
	venueStepAutoBookingCourts  // ordered subset of courts for auto-booking; only shown when auto-booking is enabled
	venueStepBookingOpensDays   // days in advance booking opens
)

// venueWizard holds state for the add-venue multi-step dialog.
type venueWizard struct {
	groupID            int64
	step               venueWizardStep
	name               string
	courts             string
	timeSlots          string
	preferredGameTime  string // chosen from timeSlots; empty = no preference
	address            string
	gameDays           []int  // weekday ints (0=Sun..6=Sat)
	gracePeriod        int    // hours, 0 means use default (24)
	autoBookingEnabled bool   // whether automatic court booking is enabled
	autoBookingCourts  string // ordered subset of courts for auto-booking; empty = any
	bookingOpensDays   int    // days in advance booking opens; 0 means use default (14)
}

// venueEditField identifies which venue field is being edited.
type venueEditField int

const (
	venueEditFieldName venueEditField = iota
	venueEditFieldCourts
	venueEditFieldTimeSlots
	venueEditFieldAddress
	venueEditFieldGameDays
	venueEditFieldGracePeriod
	venueEditFieldPreferredTime
	venueEditFieldAutoBookingCourts
	venueEditFieldBookingOpensDays
)

// venueEditState tracks an in-progress single-field edit for an existing venue.
type venueEditState struct {
	venueID int64
	groupID int64
	field   venueEditField
}

// venueGameDaysEditState holds the toggle state when editing game days for an existing venue.
type venueGameDaysEditState struct {
	venueID      int64
	groupID      int64
	selectedDays []int
	msgID        int
}

// venuePreferredTimeEditState holds state when editing the preferred game time for an existing venue.
// The admin picks from the venue's time_slots via inline buttons.
type venuePreferredTimeEditState struct {
	venueID   int64
	groupID   int64
	timeSlots []string // available slots to choose from
}

// groupVenuePickState holds game creation data for a multi-group admin who has
// selected a group and is now choosing a venue for that group.
// Keyed by private chat ID in pendingGroupVenuePick.
type groupVenuePickState struct {
	groupID     int64
	gameDate    time.Time
	courts      string
	replyChatID int64
	replyMsgID  int
}

// manageCourtsToggleState holds the in-progress court-toggle state when an admin
// is editing courts for an existing game via inline buttons.
// Keyed by private chat ID in pendingManageCourtsToggle.
type manageCourtsToggleState struct {
	gameID         int64
	venueCourts    []string
	selectedCourts map[string]bool
}

// venueCredStep tracks which input the credential-add wizard is waiting for.
type venueCredStep int

const (
	venueCredStepLogin     venueCredStep = iota // waiting for login (email)
	venueCredStepPriority                       // waiting for priority integer
	venueCredStepMaxCourts                      // waiting for max-courts per booking
	venueCredStepPassword                       // waiting for password (deleted immediately)
)

// venueCredWizard holds state for the add-credential multi-step dialog.
// Keyed by private chat ID in pendingVenueCredAdd.
type venueCredWizard struct {
	venueID   int64
	groupID   int64
	login     string
	priority  int
	maxCourts int // 0 = use default (3)
	step      venueCredStep
	suggested int   // suggested next priority (fetched after login step)
	inUse     []int // priorities already in use
}

// maxConcurrentHandlers caps the number of update goroutines running in parallel.
// This prevents memory exhaustion if Telegram delivers a burst of updates while
// the DB or network is slow.
const maxConcurrentHandlers = 50

type Bot struct {
	api                           *tgbotapi.BotAPI
	client                        client.ManagementClient
	serviceAdminIDs               map[int64]bool
	loc                           *time.Location
	logger                        *slog.Logger
	pendingGames                  sync.Map      // map[pendingGameKey]*pendingGame
	pendingCourtsEdit             sync.Map      // map[chatID int64]gameID int64
	pendingManageCourtsToggle     sync.Map      // map[chatID int64]*manageCourtsToggleState
	pendingNewGameWizard          sync.Map      // map[chatID int64]*newGameWizard
	pendingVenueWizard            sync.Map      // map[chatID int64]*venueWizard
	pendingVenueEdit              sync.Map      // map[chatID int64]*venueEditState
	pendingVenueGameDaysEdit      sync.Map      // map[chatID int64]*venueGameDaysEditState
	pendingVenuePreferredTimeEdit sync.Map      // map[chatID int64]*venuePreferredTimeEditState
	pendingGroupVenuePick         sync.Map      // map[chatID int64]*groupVenuePickState
	pendingVenueCredAdd           sync.Map      // map[chatID int64]*venueCredWizard
	handlerSem                    chan struct{} // semaphore limiting concurrent update handlers
	callbackRouter                map[string]callbackHandler
}

func New(api *tgbotapi.BotAPI, loc *time.Location, mgmtClient client.ManagementClient, serviceAdminIDs string, logger *slog.Logger) *Bot {
	b := &Bot{
		api:             api,
		client:          mgmtClient,
		serviceAdminIDs: parseServiceAdminIDs(serviceAdminIDs),
		loc:             loc,
		logger:          logger,
		handlerSem:      make(chan struct{}, maxConcurrentHandlers),
	}
	b.callbackRouter = b.buildCallbackRouter()
	return b
}

// parseServiceAdminIDs converts a comma-separated string of Telegram user IDs
// into a set for O(1) lookup. Invalid entries are logged and skipped.
func parseServiceAdminIDs(s string) map[int64]bool {
	ids := make(map[int64]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			slog.Warn("SERVICE_ADMIN_IDS: ignoring invalid entry", "value", part)
			continue
		}
		ids[id] = true
	}
	return ids
}

// Start runs the long-polling update loop until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query", "my_chat_member"}

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			// Block until a handler slot is free, but still honour context cancellation.
			// This provides backpressure rather than silently dropping updates; Telegram
			// will buffer additional updates server-side while we are busy.
			select {
			case b.handlerSem <- struct{}{}:
			case <-ctx.Done():
				b.api.StopReceivingUpdates()
				return
			}
			go func() {
				defer func() { <-b.handlerSem }()
				b.processUpdate(ctx, update)
			}()
		}
	}
}

func (b *Bot) processUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("panic in update handler", "recover", r)
		}
	}()

	switch {
	case update.Message != nil:
		slog.Debug("incoming message", "from", update.Message.From.ID, "chat", update.Message.Chat.ID)
		if update.Message.Chat.IsGroup() || update.Message.Chat.IsSuperGroup() {
			b.reconcileGroupIfUnknown(ctx, update.Message.Chat)
		}
		b.handleMessage(ctx, update.Message)
	case update.CallbackQuery != nil:
		slog.Debug("incoming callback", "from", update.CallbackQuery.From.ID, "data", update.CallbackQuery.Data)
		b.handleCallback(ctx, update.CallbackQuery)
	case update.MyChatMember != nil:
		slog.Debug("my_chat_member update", "chat", update.MyChatMember.Chat.ID, "new_status", update.MyChatMember.NewChatMember.Status)
		b.handleMyChatMember(ctx, update.MyChatMember)
	default:
		slog.Debug("unhandled update type", "update_id", update.UpdateID)
	}
}

func (b *Bot) handleMyChatMember(ctx context.Context, update *tgbotapi.ChatMemberUpdated) {
	chat := update.Chat
	if chat.Type != "group" && chat.Type != "supergroup" {
		return
	}

	newStatus := update.NewChatMember.Status
	oldStatus := update.OldChatMember.Status

	// Use the language of the person who triggered the membership change.
	lz := b.userLocalizer(update.From.LanguageCode)

	switch newStatus {
	case "left", "kicked":
		if err := b.client.RemoveGroup(ctx, chat.ID); err != nil {
			slog.Error("handleMyChatMember: remove group", "chat_id", chat.ID, "err", err)
		}
		slog.Info("Bot removed from group", "chat_id", chat.ID, "title", chat.Title)

	case "member", "administrator":
		isAdmin := newStatus == "administrator"

		if err := b.client.UpsertGroup(ctx, chat.ID, chat.Title, isAdmin); err != nil {
			slog.Error("handleMyChatMember: upsert group", "chat_id", chat.ID, "err", err)
		}
		slog.Info("Bot membership changed", "chat_id", chat.ID, "title", chat.Title,
			"old_status", oldStatus, "new_status", newStatus)

		if text := membershipNotifyText(oldStatus, newStatus, chat.Title, lz); text != "" {
			msg := tgbotapi.NewMessage(update.From.ID, text)
			if _, err := b.api.Send(msg); err != nil {
				slog.Error("handleMyChatMember: notify permission change", "user_id", update.From.ID, "err", err)
			}
		}
	}
}

// reconcileGroupIfUnknown lazily registers a group the bot is already a member
// of but has not yet stored in bot_groups. This handles the upgrade path where
// Telegram does not replay my_chat_member events for pre-existing memberships:
// the first message the bot receives from an unregistered group triggers a live
// Telegram API call to fetch admin status and upsert the row.
func (b *Bot) reconcileGroupIfUnknown(ctx context.Context, chat *tgbotapi.Chat) {
	ok, err := b.client.GroupExists(ctx, chat.ID)
	if err != nil {
		slog.Error("reconcileGroup: existence check", "chat_id", chat.ID, "err", err)
		return
	}
	if ok {
		return
	}

	member, err := b.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{ChatID: chat.ID, UserID: b.api.Self.ID},
	})
	if err != nil {
		slog.Warn("reconcileGroup: GetChatMember failed", "chat_id", chat.ID, "err", err)
		return
	}
	if member.Status == "left" || member.Status == "kicked" {
		return
	}
	isAdmin := member.Status == "administrator" || member.Status == "creator"
	title := chat.Title
	if title == "" {
		title = fmt.Sprintf("Group %d", chat.ID)
	}
	if err := b.client.UpsertGroup(ctx, chat.ID, title, isAdmin); err != nil {
		slog.Error("reconcileGroup: upsert", "chat_id", chat.ID, "err", err)
		return
	}
	slog.Info("reconcileGroup: registered previously-unknown group",
		"chat_id", chat.ID, "title", title, "bot_is_admin", isAdmin)
}

// membershipNotifyText returns the DM text to send to the person who triggered a
// bot membership change, or an empty string when no notification is needed.
// It is a pure function with no side effects so it can be tested without mocks.
func membershipNotifyText(oldStatus, newStatus, chatTitle string, lz *i18n.Localizer) string {
	// Only notify on transitions that land the bot in a non-admin member state.
	if newStatus != "member" {
		return ""
	}
	wasAbsent := oldStatus == "left" || oldStatus == "kicked"
	wasAdmin := oldStatus == "administrator" || oldStatus == "creator"
	switch {
	case wasAbsent:
		return lz.Tf(i18n.MsgAddedNoAdmin, chatTitle)
	case wasAdmin:
		return lz.Tf(i18n.MsgLostAdmin, chatTitle)
	default:
		return ""
	}
}

// ── Language resolution ───────────────────────────────────────────────────────

// userLocalizer returns a Localizer based on a Telegram user's LanguageCode.
func (b *Bot) userLocalizer(langCode string) *i18n.Localizer {
	return i18n.New(i18n.Normalize(langCode))
}

// groupLocalizer fetches the stored language for a group and returns a Localizer.
// Falls back to English if the group is not found or the call fails.
func (b *Bot) groupLocalizer(ctx context.Context, chatID int64) *i18n.Localizer {
	group, err := b.client.GetGroupByID(ctx, chatID)
	if err != nil || group == nil {
		return i18n.New(i18n.En)
	}
	return i18n.New(i18n.Normalize(group.Language))
}

// wizardLoc returns the timezone stored in the wizard, falling back to b.loc.
// Use this everywhere the wizard needs a *time.Location after the group is known.
func (b *Bot) wizardLoc(w *newGameWizard) *time.Location {
	if w.loc != nil {
		return w.loc
	}
	return b.loc
}

// groupLocation loads the IANA timezone for a group and returns a *time.Location.
// Falls back to b.loc if the group is not found or the timezone string is invalid.
func (b *Bot) groupLocation(ctx context.Context, chatID int64) *time.Location {
	group, err := b.client.GetGroupByID(ctx, chatID)
	if err != nil || group == nil {
		return b.loc
	}
	loc, err := time.LoadLocation(group.Timezone)
	if err != nil {
		return b.loc
	}
	return loc
}

package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
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
	replyChatID int64
	replyMsgID  int
}

// maxConcurrentHandlers caps the number of update goroutines running in parallel.
// This prevents memory exhaustion if Telegram delivers a burst of updates while
// the DB or network is slow.
const maxConcurrentHandlers = 50

type Bot struct {
	api               *tgbotapi.BotAPI
	gameService       *service.GameService
	partService       *service.ParticipationService
	groupRepo         *storage.GroupRepo
	loc               *time.Location
	logger            *slog.Logger
	pendingGames      sync.Map    // map[pendingGameKey]*pendingGame
	pendingCourtsEdit sync.Map    // map[chatID int64]gameID int64
	handlerSem        chan struct{} // semaphore limiting concurrent update handlers
}

func New(api *tgbotapi.BotAPI, loc *time.Location, gameService *service.GameService, partService *service.ParticipationService, groupRepo *storage.GroupRepo, logger *slog.Logger) *Bot {
	return &Bot{
		api:         api,
		gameService: gameService,
		partService: partService,
		groupRepo:   groupRepo,
		loc:         loc,
		logger:      logger,
		handlerSem:  make(chan struct{}, maxConcurrentHandlers),
	}
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

	switch newStatus {
	case "left", "kicked":
		if err := b.groupRepo.Remove(ctx, chat.ID); err != nil {
			slog.Error("handleMyChatMember: remove group", "chat_id", chat.ID, "err", err)
		}
		slog.Info("Bot removed from group", "chat_id", chat.ID, "title", chat.Title)

	case "member", "administrator":
		isAdmin := newStatus == "administrator"

		if err := b.groupRepo.Upsert(ctx, chat.ID, chat.Title, isAdmin); err != nil {
			slog.Error("handleMyChatMember: upsert group", "chat_id", chat.ID, "err", err)
		}
		slog.Info("Bot membership changed", "chat_id", chat.ID, "title", chat.Title,
			"old_status", oldStatus, "new_status", newStatus)

		if text := membershipNotifyText(oldStatus, newStatus, chat.Title); text != "" {
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
	ok, err := b.groupRepo.Exists(ctx, chat.ID)
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
	if err := b.groupRepo.Upsert(ctx, chat.ID, title, isAdmin); err != nil {
		slog.Error("reconcileGroup: upsert", "chat_id", chat.ID, "err", err)
		return
	}
	slog.Info("reconcileGroup: registered previously-unknown group",
		"chat_id", chat.ID, "title", title, "bot_is_admin", isAdmin)
}

// membershipNotifyText returns the DM text to send to the person who triggered a
// bot membership change, or an empty string when no notification is needed.
// It is a pure function with no side effects so it can be tested without mocks.
func membershipNotifyText(oldStatus, newStatus, chatTitle string) string {
	// Only notify on transitions that land the bot in a non-admin member state.
	if newStatus != "member" {
		return ""
	}
	wasAbsent := oldStatus == "left" || oldStatus == "kicked"
	wasAdmin := oldStatus == "administrator" || oldStatus == "creator"
	switch {
	case wasAbsent:
		return fmt.Sprintf(
			"I've been added to \"%s\" but I don't have administrator permissions.\n\n"+
				"To pin game announcements, please grant me admin rights in that group.",
			chatTitle,
		)
	case wasAdmin:
		return fmt.Sprintf(
			"I've lost administrator permissions in \"%s\".\n\n"+
				"Without admin rights I can no longer pin game announcements.",
			chatTitle,
		)
	default:
		return ""
	}
}

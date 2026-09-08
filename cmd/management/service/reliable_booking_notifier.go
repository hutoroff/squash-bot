package service

import (
	"context"
	"fmt"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

type reliableBookingNotificationRepository interface {
	ClaimPendingNotifications(context.Context, int) ([]*models.ManagementBookingNotification, error)
	MarkNotificationDelivered(context.Context, int64) error
}

// ReliableBookingNotifier delivers each durable state-change notification at
// most once. Claims are committed before Telegram calls, so a crash can lose a
// notification but cannot create an unbounded notification loop.
type ReliableBookingNotifier struct {
	repository reliableBookingNotificationRepository
	api        TelegramAPI
	logger     *slog.Logger
	batchSize  int
}

func NewReliableBookingNotifier(repository reliableBookingNotificationRepository, api TelegramAPI, logger *slog.Logger, batchSize int) (*ReliableBookingNotifier, error) {
	if repository == nil || api == nil || logger == nil || batchSize <= 0 || batchSize > 100 {
		return nil, fmt.Errorf("valid reliable booking notifier dependencies are required")
	}
	return &ReliableBookingNotifier{repository: repository, api: api, logger: logger, batchSize: batchSize}, nil
}

func (n *ReliableBookingNotifier) RunOnce(ctx context.Context) error {
	notifications, err := n.repository.ClaimPendingNotifications(ctx, n.batchSize)
	if err != nil {
		return err
	}
	var firstErr error
	for _, notification := range notifications {
		if err := n.deliver(ctx, notification); err != nil {
			n.logger.Warn("reliable booking operator notification failed", "notification_id", notification.ID, "kind", notification.Kind, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := n.repository.MarkNotificationDelivered(ctx, notification.ID); err != nil {
			n.logger.Warn("mark reliable booking operator notification delivered", "notification_id", notification.ID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (n *ReliableBookingNotifier) deliver(ctx context.Context, notification *models.ManagementBookingNotification) error {
	admins, err := n.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: notification.GroupID}})
	if err != nil {
		return fmt.Errorf("read group administrators: %w", err)
	}
	text := fmt.Sprintf("Booking attention required: %s; venue %d; date %s %s; operation %s.",
		notification.Kind, notification.VenueID, notification.GameDate.Format("2006-01-02"), notification.GameTime, notification.OperationID)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User == nil || admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		message := tgbotapi.NewMessage(admin.User.ID, text)
		message.DisableNotification = true
		if _, err := n.api.Send(message); err != nil {
			return fmt.Errorf("send administrator notification: %w", err)
		}
	}
	return nil
}

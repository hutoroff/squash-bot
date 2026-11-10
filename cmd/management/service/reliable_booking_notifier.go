package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

type reliableBookingNotificationRepository interface {
	ClaimPendingNotifications(context.Context, int) ([]*models.ManagementBookingNotification, error)
	MarkNotificationDelivered(context.Context, int64) error
}

// ReliableBookingNotifier delivers each durable state-change notification at
// most once. Claims are committed before Telegram calls, so a crash can lose a
// notification but cannot create an unbounded notification loop. Notifications
// claimed together for the same intent and kind are sent as one localized DM.
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

type bookingNotificationGroup struct {
	notifications []*models.ManagementBookingNotification
}

func (n *ReliableBookingNotifier) RunOnce(ctx context.Context) error {
	notifications, err := n.repository.ClaimPendingNotifications(ctx, n.batchSize)
	if err != nil {
		return err
	}
	var firstErr error
	for _, group := range groupBookingNotifications(notifications) {
		first := group.notifications[0]
		if err := n.deliver(group.notifications); err != nil {
			n.logger.Warn("reliable booking operator notification failed", "notification_id", first.ID, "intent_id", first.IntentID, "kind", first.Kind, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, notification := range group.notifications {
			if err := n.repository.MarkNotificationDelivered(ctx, notification.ID); err != nil {
				n.logger.Warn("mark reliable booking operator notification delivered", "notification_id", notification.ID, "err", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

// groupBookingNotifications keeps claim order and merges notifications of the
// same intent and kind.
func groupBookingNotifications(notifications []*models.ManagementBookingNotification) []*bookingNotificationGroup {
	var groups []*bookingNotificationGroup
	byKey := make(map[string]*bookingNotificationGroup)
	for _, notification := range notifications {
		key := fmt.Sprintf("%d:%s", notification.IntentID, notification.Kind)
		group, ok := byKey[key]
		if !ok {
			group = &bookingNotificationGroup{}
			byKey[key] = group
			groups = append(groups, group)
		}
		group.notifications = append(group.notifications, notification)
	}
	return groups
}

func (n *ReliableBookingNotifier) deliver(notifications []*models.ManagementBookingNotification) error {
	first := notifications[0]
	operationIDs := make([]string, 0, len(notifications))
	for _, notification := range notifications {
		operationIDs = append(operationIDs, string(notification.OperationID))
	}
	// The scheduled auto-booking job already reports a shortfall in its own
	// localized summary, so a scheduled partial notification is not repeated.
	if first.Kind == models.ManagementBookingNotificationPartial && first.Source == models.BookingIntentScheduled {
		n.logger.Info("reliable booking notification covered by auto-booking summary",
			"intent_id", first.IntentID, "kind", first.Kind, "operation_ids", operationIDs)
		return nil
	}
	admins, err := n.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: first.GroupID}})
	if err != nil {
		return fmt.Errorf("read group administrators: %w", err)
	}
	text := bookingNotificationText(notifications)
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
	n.logger.Info("reliable booking notification delivered", "intent_id", first.IntentID, "kind", first.Kind,
		"venue_id", first.VenueID, "operation_ids", operationIDs, "recipients", len(seen))
	return nil
}

func bookingNotificationText(notifications []*models.ManagementBookingNotification) string {
	first := notifications[0]
	lz := i18n.New(i18n.Normalize(first.Language))
	venue := first.VenueName
	if venue == "" {
		venue = fmt.Sprintf("#%d", first.VenueID)
	}
	date := first.GameDate.Format("2006-01-02")
	var key string
	switch first.Kind {
	case models.ManagementBookingNotificationSuccessWarning:
		key = i18n.BookingAttentionSuccessWarning
	case models.ManagementBookingNotificationOutcomeUnknown:
		key = i18n.BookingAttentionOutcomeUnknown
	case models.ManagementBookingNotificationMissedAction:
		key = i18n.BookingAttentionMissedAction
	case models.ManagementBookingNotificationPossibleDuplicate:
		key = i18n.BookingAttentionPossibleDuplicate
	default:
		return lz.Tf(i18n.BookingAttentionPartial, venue, date, first.GameTime)
	}
	operations := make(map[string]bool)
	for _, notification := range notifications {
		operations[string(notification.OperationID)] = true
	}
	return lz.Tf(key, venue, date, first.GameTime, len(operations), courtLabelList(notifications))
}

func courtLabelList(notifications []*models.ManagementBookingNotification) string {
	seen := make(map[string]bool)
	var labels []string
	for _, notification := range notifications {
		if label := strings.TrimSpace(notification.CourtLabel); label != "" && !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "—"
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

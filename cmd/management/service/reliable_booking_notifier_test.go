package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type notificationRepositoryFake struct {
	notifications []*models.ManagementBookingNotification
	claims        int
	delivered     []int64
}

func (r *notificationRepositoryFake) ClaimPendingNotifications(context.Context, int) ([]*models.ManagementBookingNotification, error) {
	r.claims++
	if r.claims > 1 {
		return nil, nil
	}
	return r.notifications, nil
}
func (r *notificationRepositoryFake) MarkNotificationDelivered(_ context.Context, id int64) error {
	r.delivered = append(r.delivered, id)
	return nil
}

type notificationTelegramFake struct {
	sends int
	err   error
}

func (a *notificationTelegramFake) Send(tgbotapi.Chattable) (tgbotapi.Message, error) {
	a.sends++
	return tgbotapi.Message{}, a.err
}
func (*notificationTelegramFake) Request(tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return nil, errors.New("unexpected request")
}
func (*notificationTelegramFake) GetChatAdministrators(tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return []tgbotapi.ChatMember{{User: &tgbotapi.User{ID: 42}}, {User: &tgbotapi.User{ID: 42}}, {User: &tgbotapi.User{ID: 99, IsBot: true}}}, nil
}

func TestReliableBookingNotifierClaimsBeforeOneBoundedDelivery(t *testing.T) {
	repository := &notificationRepositoryFake{notifications: []*models.ManagementBookingNotification{{
		ID: 7, OperationID: bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e0712042e"),
		Kind: models.ManagementBookingNotificationOutcomeUnknown, GroupID: 100, VenueID: 2,
		GameDate: time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC), GameTime: "18:00",
	}}}
	api := &notificationTelegramFake{}
	notifier, err := NewReliableBookingNotifier(repository, api, slog.Default(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := notifier.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.sends != 1 || len(repository.delivered) != 1 || repository.delivered[0] != 7 {
		t.Fatalf("sends = %d delivered = %v", api.sends, repository.delivered)
	}
}

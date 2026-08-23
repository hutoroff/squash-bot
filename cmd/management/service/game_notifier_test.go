package service

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

type notifierRaceGameRepo struct {
	GameRepository
	mu   sync.Mutex
	game models.Game
}

func (r *notifierRaceGameRepo) GetByID(_ context.Context, _ int64) (*models.Game, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	game := r.game
	return &game, nil
}

func (r *notifierRaceGameRepo) setCourts(courts string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.game.Courts = courts
	r.game.CourtsCount = count
}

type notifierRacePartRepo struct{ ParticipationRepository }

func (r *notifierRacePartRepo) GetByGame(_ context.Context, _ int64) ([]*models.GameParticipation, error) {
	return nil, nil
}

type notifierRaceGuestRepo struct{ GuestRepository }

func (r *notifierRaceGuestRepo) GetByGame(_ context.Context, _ int64) ([]*models.GuestParticipation, error) {
	return nil, nil
}

type notifierRaceGroupRepo struct {
	GroupRepository
	group models.Group
}

func (r *notifierRaceGroupRepo) GetByID(_ context.Context, _ int64) (*models.Group, error) {
	group := r.group
	return &group, nil
}

// blockingNotifierAPI simulates an old Telegram edit that remains in flight
// while a newer edit completes. The displayed text changes when each request
// completes, matching the last-write-wins behavior seen by Telegram users.
type blockingNotifierAPI struct {
	mu            sync.Mutex
	calls         int
	displayedText string
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

func newBlockingNotifierAPI() *blockingNotifierAPI {
	return &blockingNotifierAPI{
		firstEntered:  make(chan struct{}),
		secondEntered: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
}

func (a *blockingNotifierAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	edit, ok := c.(tgbotapi.EditMessageTextConfig)
	if !ok {
		return tgbotapi.Message{}, nil
	}

	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()

	switch call {
	case 1:
		close(a.firstEntered)
		<-a.releaseFirst
	case 2:
		close(a.secondEntered)
	}

	a.mu.Lock()
	a.displayedText = edit.Text
	a.mu.Unlock()
	return tgbotapi.Message{}, nil
}

func (a *blockingNotifierAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (a *blockingNotifierAPI) GetChatAdministrators(_ tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return nil, nil
}

func (a *blockingNotifierAPI) displayed() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.displayedText
}

func TestGameNotifier_SerializesFetchAndEditPerGame(t *testing.T) {
	messageID := int64(42)
	gameRepo := &notifierRaceGameRepo{game: models.Game{
		ID:          19,
		ChatID:      100,
		MessageID:   &messageID,
		GameDate:    time.Now().Add(24 * time.Hour),
		Courts:      "8,9,10",
		CourtsCount: 3,
	}}
	api := newBlockingNotifierAPI()
	notifier := NewGameNotifier(
		api,
		gameRepo,
		&notifierRacePartRepo{},
		&notifierRaceGuestRepo{},
		&notifierRaceGroupRepo{group: models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}},
		time.UTC,
		testLogger,
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		notifier.EditGameMessage(context.Background(), 19)
	}()
	<-api.firstEntered // the first request rendered the old three-court state

	gameRepo.setCourts("8,10", 2) // scheduler cancels court 9
	wg.Add(1)
	go func() {
		defer wg.Done()
		notifier.EditGameMessage(context.Background(), 19)
	}()

	// Before serialization, the second request entered Telegram while the stale
	// first request was blocked, then the stale request completed last. With the
	// per-game lock, the second request waits and therefore always completes last.
	select {
	case <-api.secondEntered:
	case <-time.After(50 * time.Millisecond):
	}
	close(api.releaseFirst)
	wg.Wait()

	got := api.displayed()
	if !strings.Contains(got, "8,10") {
		t.Fatalf("final announcement does not contain updated courts: %q", got)
	}
	if strings.Contains(got, "8,9,10") {
		t.Fatalf("stale court list overwrote the updated announcement: %q", got)
	}
}

func TestResolveGroupTimezone(t *testing.T) {
	utc := time.UTC
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}

	cases := []struct {
		name       string
		group      *models.Group
		defaultLoc *time.Location
		wantLoc    *time.Location
	}{
		{
			name:       "empty timezone falls back to default",
			group:      &models.Group{Timezone: ""},
			defaultLoc: utc,
			wantLoc:    utc,
		},
		{
			name:       "valid IANA timezone is used",
			group:      &models.Group{Timezone: "Europe/Berlin"},
			defaultLoc: utc,
			wantLoc:    berlin,
		},
		{
			name:       "invalid IANA timezone falls back to default",
			group:      &models.Group{Timezone: "Not/A/Timezone"},
			defaultLoc: utc,
			wantLoc:    utc,
		},
		{
			name:       "invalid timezone falls back to non-UTC default",
			group:      &models.Group{Timezone: "garbage"},
			defaultLoc: berlin,
			wantLoc:    berlin,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveGroupTimezone(tc.group, tc.defaultLoc, testLogger)
			if got.String() != tc.wantLoc.String() {
				t.Errorf("resolveGroupTimezone: want %q, got %q", tc.wantLoc, got)
			}
		})
	}
}

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// countingTelegramAPI records how many GetChatAdministrators calls were made
// and reports adminID as an administrator of every group.
type countingTelegramAPI struct {
	adminID int64
	calls   int
}

func (c *countingTelegramAPI) Send(_ tgbotapi.Chattable) (tgbotapi.Message, error) {
	return tgbotapi.Message{}, nil
}

func (c *countingTelegramAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (c *countingTelegramAPI) GetChatAdministrators(_ tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	c.calls++
	return []tgbotapi.ChatMember{{User: &tgbotapi.User{ID: c.adminID}}}, nil
}

// identityUserRepo is a UserRepository fake whose TelegramID is the identity
// function, so tests can keep using one numeric constant for both userID and
// the Telegram ID GetChatAdministrators matches against.
type identityUserRepo struct{}

func (identityUserRepo) GetByID(_ context.Context, userID int64) (*models.User, error) {
	return &models.User{ID: userID}, nil
}

func (identityUserRepo) TelegramID(_ context.Context, userID int64) (int64, error) {
	return userID, nil
}

func newTestResolver(adminID int64, chatIDs ...int64) (*AdminGroupsResolver, *countingTelegramAPI) {
	groups := make([]models.Group, 0, len(chatIDs))
	for _, id := range chatIDs {
		groups = append(groups, models.Group{ChatID: id})
	}
	api := &countingTelegramAPI{adminID: adminID}
	r := NewAdminGroupsResolver(
		&stubGroupRepoAnnounce{groups: groups},
		identityUserRepo{},
		api,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return r, api
}

func TestAdminGroupsFor_CachesWithinTTL(t *testing.T) {
	r, api := newTestResolver(42, -100, -200)

	first, err := r.AdminGroupsFor(context.Background(), 42)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("want 2 admin groups, got %v", first)
	}
	if api.calls != 2 {
		t.Fatalf("want 2 Telegram calls, got %d", api.calls)
	}

	second, err := r.AdminGroupsFor(context.Background(), 42)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if api.calls != 2 {
		t.Errorf("cached call should not hit Telegram: got %d calls", api.calls)
	}
	if len(second) != 2 {
		t.Errorf("cached result mismatch: %v", second)
	}
}

func TestAdminGroupsFor_ExpiredCacheRefetches(t *testing.T) {
	r, api := newTestResolver(42, -100)
	r.ttl = 0 // every entry is immediately stale

	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if api.calls != 2 {
		t.Errorf("want 2 Telegram calls after TTL expiry, got %d", api.calls)
	}
}

// blockingTelegramAPI holds the first caller inside GetChatAdministrators until
// release is closed, so concurrent resolver misses can be observed.
type blockingTelegramAPI struct {
	adminID int64
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	failFor int64 // chat ID that always errors; 0 disables
}

func (b *blockingTelegramAPI) Send(_ tgbotapi.Chattable) (tgbotapi.Message, error) {
	return tgbotapi.Message{}, nil
}

func (b *blockingTelegramAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (b *blockingTelegramAPI) GetChatAdministrators(c tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	b.calls.Add(1)
	if b.release != nil {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	if c.ChatID == b.failFor {
		return nil, errors.New("rate limited")
	}
	return []tgbotapi.ChatMember{{User: &tgbotapi.User{ID: b.adminID}}}, nil
}

func newBlockingResolver(api *blockingTelegramAPI, chatIDs ...int64) *AdminGroupsResolver {
	groups := make([]models.Group, 0, len(chatIDs))
	for _, id := range chatIDs {
		groups = append(groups, models.Group{ChatID: id})
	}
	return NewAdminGroupsResolver(
		&stubGroupRepoAnnounce{groups: groups},
		identityUserRepo{},
		api,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

// Several parallel requests on a cold cache must trigger one group scan, not one per request.
func TestAdminGroupsFor_CoalescesConcurrentMisses(t *testing.T) {
	api := &blockingTelegramAPI{adminID: 42, entered: make(chan struct{}), release: make(chan struct{})}
	r := newBlockingResolver(api, -100, -200)

	const callers = 5
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
				t.Errorf("AdminGroupsFor: %v", err)
			}
		}()
	}

	<-api.entered                     // the leader is inside Telegram
	time.Sleep(50 * time.Millisecond) // let the followers reach the resolver
	close(api.release)
	wg.Wait()

	if got := api.calls.Load(); got != 2 {
		t.Errorf("want 2 Telegram calls (one per group), got %d", got)
	}
}

// A scan that lost a group to a Telegram error must not be cached for the whole TTL.
func TestAdminGroupsFor_PartialResultNotCached(t *testing.T) {
	api := &blockingTelegramAPI{adminID: 42, failFor: -200}
	r := newBlockingResolver(api, -100, -200)

	first, err := r.AdminGroupsFor(context.Background(), 42)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("want the one reachable group, got %v", first)
	}

	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := api.calls.Load(); got != 4 {
		t.Errorf("partial result should be refetched, want 4 Telegram calls, got %d", got)
	}
}

func TestAdminGroupsFor_CacheIsPerUser(t *testing.T) {
	r, api := newTestResolver(42, -100)

	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("admin call: %v", err)
	}
	other, err := r.AdminGroupsFor(context.Background(), 99)
	if err != nil {
		t.Fatalf("other call: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("non-admin should have no groups, got %v", other)
	}
	if api.calls != 2 {
		t.Errorf("want a fresh lookup per user, got %d calls", api.calls)
	}
}

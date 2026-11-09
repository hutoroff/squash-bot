package telegram

import (
	"context"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	managementclient "github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

type courtsManagementClient struct {
	gameManagementClient

	courtsMu      sync.Mutex
	updateCalls   []string // plain UpdateCourts
	cancelCalls   []string // UpdateCourtsAndCancelBookings
	activeBooking []managementclient.CourtBookingInfo
}

func (f *courtsManagementClient) ListActiveCourtBookings(context.Context, int64, []string) ([]managementclient.CourtBookingInfo, error) {
	return f.activeBooking, nil
}

func (f *courtsManagementClient) UpdateCourts(_ context.Context, _, _ int64, courts, _ string, _ int64) error {
	f.courtsMu.Lock()
	defer f.courtsMu.Unlock()
	f.updateCalls = append(f.updateCalls, courts)
	return nil
}

func (f *courtsManagementClient) UpdateCourtsAndCancelBookings(_ context.Context, _, _ int64, newCourts, _ string, _ int64, _ string) ([]string, []managementclient.CancelFailure, error) {
	f.courtsMu.Lock()
	defer f.courtsMu.Unlock()
	f.cancelCalls = append(f.cancelCalls, newCourts)
	return nil, nil, nil
}

type sentMessage struct{ chatID, text, replyTo string }

func newLastCourtBot(t *testing.T) (*Bot, *courtsManagementClient, func() ([]sentMessage, []string)) {
	t.Helper()
	const adminID = int64(77)
	var mu sync.Mutex
	var sent []sentMessage
	var edits []string
	api := fakeTelegramAPIWithAdmins(t, adminID, func(r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		switch path.Base(r.URL.Path) {
		case "sendMessage":
			sent = append(sent, sentMessage{r.Form.Get("chat_id"), r.Form.Get("text"), r.Form.Get("reply_to_message_id")})
		case "editMessageText":
			if r.Form.Get("chat_id") == "77" {
				edits = append(edits, r.Form.Get("text"))
			}
		}
	})
	announcement := int64(321)
	mgmt := &courtsManagementClient{gameManagementClient: gameManagementClient{
		game: &models.Game{
			ID: 17, ChatID: -10017, MessageID: &announcement, Sport: "squash",
			GameDate: time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC), Courts: "5", CourtsCount: 1,
		},
		group:    &models.Group{ChatID: -10017, Language: string(i18n.En), Timezone: "UTC"},
		resolved: &managementclient.ResolvedUser{UserID: 7007, DisplayName: "Admin"},
	}}
	b := &Bot{api: api, client: mgmt, loc: time.UTC, logger: telegramNoopLogger()}
	b.userLangCache.Store(adminID, userLangPref{lang: i18n.En, hasOverride: true})
	b.callbackRouter = b.buildCallbackRouter()
	// The admin has just unticked the only court.
	b.pendingManageCourtsToggle.Store(adminID, &manageCourtsToggleState{
		gameID: 17, venueCourts: []string{"5", "6"}, selectedCourts: map[string]bool{"5": false},
	})
	snapshot := func() ([]sentMessage, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentMessage(nil), sent...), append([]string(nil), edits...)
	}
	return b, mgmt, snapshot
}

func TestManageCourtsConfirm_EmptySelectionAsksBeforeRemovingLastCourt(t *testing.T) {
	b, mgmt, snapshot := newLastCourtBot(t)

	b.callbackRouter["manage_court_confirm"](context.Background(), callbackQuery(77, "manage_court_confirm:17"), "17")

	if len(mgmt.updateCalls) != 0 || len(mgmt.cancelCalls) != 0 {
		t.Fatalf("courts changed before confirmation: update=%v cancel=%v", mgmt.updateCalls, mgmt.cancelCalls)
	}
	_, edits := snapshot()
	lz := i18n.New(i18n.En)
	want := lz.Tf(i18n.MsgCourtRemoveAllPrompt, gameformat.UnitName("squash", lz), "5")
	if len(edits) != 1 || edits[0] != want {
		t.Fatalf("prompt edits = %q, want %q", edits, want)
	}
}

func TestManageCourtsCancelConfirm_RemoveAllPersistsEmptyAndNotifiesGroup(t *testing.T) {
	b, mgmt, snapshot := newLastCourtBot(t)
	ctx := context.Background()

	b.callbackRouter["manage_court_confirm"](ctx, callbackQuery(77, "manage_court_confirm:17"), "17")
	b.callbackRouter["manage_courts_cancel_confirm"](ctx, callbackQuery(77, "manage_courts_cancel_confirm:17"), "17")
	waitForGameEditWorker(t, b, 17)

	if len(mgmt.cancelCalls) != 1 || mgmt.cancelCalls[0] != "" {
		t.Fatalf("UpdateCourtsAndCancelBookings calls = %q, want one call with empty courts", mgmt.cancelCalls)
	}
	sent, _ := snapshot()
	var group *sentMessage
	for i := range sent {
		if sent[i].chatID == "-10017" {
			group = &sent[i]
		}
	}
	if group == nil {
		t.Fatalf("no group notice sent; messages: %+v", sent)
	}
	if !strings.Contains(group.text, "will not happen") || !strings.Contains(group.text, "12.11 18:00") || group.replyTo != "321" {
		t.Errorf("group notice = %+v, want localized all-canceled text replying to the announcement", *group)
	}
}

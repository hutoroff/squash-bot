package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	managementclient "github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

type gameManagementClient struct {
	managementclient.ManagementClient

	mu             sync.Mutex
	game           *models.Game
	participations []*models.GameParticipation
	guests         []*models.GuestParticipation
	group          *models.Group
	resolved       *managementclient.ResolvedUser
	publishCalls   int
	kickCalls      []kickCall
}

type kickCall struct {
	gameID       int64
	playerID     int64
	groupID      int64
	actorUserID  int64
	actorDisplay string
}

func (f *gameManagementClient) GetGameByID(context.Context, int64) (*models.Game, error) {
	return f.game, nil
}

func (f *gameManagementClient) GetParticipations(context.Context, int64) ([]*models.GameParticipation, error) {
	return f.participations, nil
}

func (f *gameManagementClient) GetGuests(context.Context, int64) ([]*models.GuestParticipation, error) {
	return f.guests, nil
}

func (f *gameManagementClient) GetGroupByID(context.Context, int64) (*models.Group, error) {
	return f.group, nil
}

func (f *gameManagementClient) ResolveUser(context.Context, int64, string, string, string) (*managementclient.ResolvedUser, error) {
	return f.resolved, nil
}

func (f *gameManagementClient) PublishGame(context.Context, int64, int64, string) (*models.Game, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishCalls++
	return f.game, nil
}

func (f *gameManagementClient) KickPlayer(_ context.Context, gameID, playerID, groupID, actorUserID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kickCalls = append(f.kickCalls, kickCall{
		gameID: gameID, playerID: playerID, groupID: groupID,
		actorUserID: actorUserID, actorDisplay: actorDisplay,
	})
	return f.participations, f.guests, playerID == 501, nil
}

func (f *gameManagementClient) snapshotCalls() (int, []kickCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.publishCalls, append([]kickCall(nil), f.kickCalls...)
}

func TestManagePublish_RechecksAdminBeforeMutation(t *testing.T) {
	var callbackText string
	api := newFakeBotAPIWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch path.Base(r.URL.Path) {
		case "getChatAdministrators":
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		case "answerCallbackQuery":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			callbackText = r.Form.Get("text")
			writeFakeTelegramOK(w)
		default:
			writeFakeTelegramOK(w)
		}
	})

	mgmt := &gameManagementClient{
		game:     &models.Game{ID: 17, ChatID: -10017},
		resolved: &managementclient.ResolvedUser{UserID: 7007, DisplayName: "Former admin"},
	}
	b := &Bot{api: api, client: mgmt, loc: time.UTC, logger: telegramNoopLogger()}
	b.userLangCache.Store(int64(77), userLangPref{lang: i18n.En, hasOverride: true})

	b.handleManagePublish(context.Background(), callbackQuery(77, "publish_game:17"), 17)

	publishCalls, _ := mgmt.snapshotCalls()
	if publishCalls != 0 {
		t.Fatalf("PublishGame calls = %d, want 0 after live admin check failed", publishCalls)
	}
	want := i18n.New(i18n.En).T(i18n.MsgLostAdminAccess)
	if callbackText != want {
		t.Errorf("callback text = %q, want %q", callbackText, want)
	}
}

func TestManageKickPlayer_LegacyTelegramIDCallbackStillWorks(t *testing.T) {
	const (
		adminTelegramID  = int64(77)
		legacyTargetID   = int64(9001)
		playerID         = int64(501)
		canonicalActorID = int64(7007)
		gameID           = int64(17)
		groupID          = int64(-10017)
	)

	api := fakeTelegramAPIWithAdmins(t, adminTelegramID, nil)
	messageID := int64(321)
	mgmt := &gameManagementClient{
		game: &models.Game{
			ID: 17, ChatID: groupID, MessageID: &messageID,
			GameDate: time.Now().Add(24 * time.Hour), Courts: "1", CourtsCount: 1,
		},
		participations: []*models.GameParticipation{{
			GameID: gameID, PlayerID: playerID, Status: models.StatusRegistered,
			Player: &models.Player{ID: playerID, TelegramID: legacyTargetID},
		}},
		group:    &models.Group{ChatID: groupID, Language: string(i18n.En), Timezone: "UTC"},
		resolved: &managementclient.ResolvedUser{UserID: canonicalActorID, DisplayName: "Admin"},
	}
	b := &Bot{api: api, client: mgmt, loc: time.UTC, logger: telegramNoopLogger()}
	b.userLangCache.Store(adminTelegramID, userLangPref{lang: i18n.En, hasOverride: true})
	b.callbackRouter = b.buildCallbackRouter()

	cb := callbackQuery(adminTelegramID, fmt.Sprintf("manage_kick:%d:%d", gameID, legacyTargetID))
	b.callbackRouter["manage_kick"](context.Background(), cb, fmt.Sprintf("%d:%d", gameID, legacyTargetID))
	waitForGameEditWorker(t, b, gameID)

	_, calls := mgmt.snapshotCalls()
	want := []kickCall{
		{gameID: gameID, playerID: legacyTargetID, groupID: groupID, actorUserID: canonicalActorID, actorDisplay: "Admin"},
		{gameID: gameID, playerID: playerID, groupID: groupID, actorUserID: canonicalActorID, actorDisplay: "Admin"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("KickPlayer calls = %+v, want legacy attempt followed by canonical player retry %+v", calls, want)
	}
}

func TestEditGameMessage_UpdatesInPlaceWithStableKeyboard(t *testing.T) {
	var editForm map[string]string
	api := newFakeBotAPIWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if path.Base(r.URL.Path) == "editMessageText" {
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			editForm = map[string]string{
				"chat_id":      r.Form.Get("chat_id"),
				"message_id":   r.Form.Get("message_id"),
				"reply_markup": r.Form.Get("reply_markup"),
			}
		}
		writeFakeTelegramOK(w)
	})

	messageID := int64(321)
	mgmt := &gameManagementClient{
		game: &models.Game{
			ID: 42, ChatID: -10042, MessageID: &messageID,
			GameDate: time.Now().Add(24 * time.Hour), Courts: "1,2", CourtsCount: 2,
		},
		group: &models.Group{ChatID: -10042, Language: string(i18n.En), Timezone: "UTC"},
	}
	b := &Bot{api: api, client: mgmt, loc: time.UTC, logger: telegramNoopLogger()}

	b.doEditGameMessage(context.Background(), 42)

	if editForm["chat_id"] != "-10042" || editForm["message_id"] != "321" {
		t.Fatalf("edit target = chat %q message %q, want existing -10042/321", editForm["chat_id"], editForm["message_id"])
	}
	var markup struct {
		InlineKeyboard [][]struct {
			CallbackData string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(editForm["reply_markup"]), &markup); err != nil {
		t.Fatalf("decode reply_markup: %v (raw %q)", err, editForm["reply_markup"])
	}
	var callbacks []string
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			callbacks = append(callbacks, button.CallbackData)
		}
	}
	wantCallbacks := []string{"join:42", "skip:42", "guest_add:42", "guest_remove:42"}
	if !reflect.DeepEqual(callbacks, wantCallbacks) {
		t.Errorf("announcement callbacks = %v, want %v", callbacks, wantCallbacks)
	}
}

func callbackQuery(userID int64, data string) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   "callback-id",
		From: &tgbotapi.User{ID: userID, LanguageCode: "en"},
		Message: &tgbotapi.Message{
			MessageID: 10,
			Chat:      &tgbotapi.Chat{ID: userID, Type: "private"},
		},
		Data: data,
	}
}

func fakeTelegramAPIWithAdmins(t *testing.T, adminID int64, onRequest func(*http.Request)) *tgbotapi.BotAPI {
	t.Helper()
	return newFakeBotAPIWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r)
		}
		if path.Base(r.URL.Path) == "getChatAdministrators" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":[{"status":"administrator","user":{"id":%d,"is_bot":false,"first_name":"Admin"}}]}`, adminID)
			return
		}
		writeFakeTelegramOK(w)
	})
}

func waitForGameEditWorker(t *testing.T, b *Bot, gameID int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		raw, ok := b.editWorkers.Load(gameID)
		if !ok {
			return
		}
		worker := raw.(*gameEditWorker)
		worker.mu.Lock()
		idle := !worker.running
		worker.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("game edit worker did not become idle")
}

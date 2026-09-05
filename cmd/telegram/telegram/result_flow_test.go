package telegram

import (
	"context"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

type resultFlowClient struct {
	client.ManagementClient
	result                                         *client.GameResultDTO
	submitted, canceled, approved, approvalMessage int
}

func (f *resultFlowClient) ResolveUser(_ context.Context, id int64, _, _, _ string) (*client.ResolvedUser, error) {
	pid, uid := int64(1), int64(701)
	if id == 200 {
		pid, uid = 2, 702
	}
	return &client.ResolvedUser{UserID: uid, PlayerID: &pid, DisplayName: "Player"}, nil
}
func (f *resultFlowClient) SubmitGameResult(_ context.Context, game, user, opp int64, winner *int64, score, display string, kind models.ScoreKind) (*client.GameResultDTO, error) {
	f.submitted++
	f.result = &client.GameResultDTO{ID: 77, GameID: game, AuthorID: 1, OpponentID: opp, WinnerID: winner, Score: score, ScoreKind: kind, Status: "pending",
		Author: &models.Player{ID: 1, UserID: user, TelegramID: 100}, Opponent: oppPlayer()}
	return f.result, nil
}
func (f *resultFlowClient) CancelGameResult(context.Context, int64, int64, string) (*client.GameResultDTO, error) {
	f.canceled++
	return f.result, nil
}
func (f *resultFlowClient) SetGameResultApprovalMessage(context.Context, int64, int64, int) error {
	f.approvalMessage++
	return nil
}
func (f *resultFlowClient) ApproveGameResult(context.Context, int64, int64, string) (*client.GameResultDTO, error) {
	f.approved++
	return f.result, nil
}
func (f *resultFlowClient) GetGameByID(context.Context, int64) (*models.Game, error) {
	return &models.Game{GameDate: time.Now()}, nil
}

func resultFlowBot(t *testing.T, lang i18n.Lang, failDM bool) (*Bot, *resultFlowClient, *resultWizard, *[]string) {
	t.Helper()
	texts := []string{}
	api := newFakeBotAPIWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if path.Base(r.URL.Path) == "sendMessage" || path.Base(r.URL.Path) == "editMessageText" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			texts = append(texts, r.Form.Get("text")+r.Form.Get("reply_markup"))
			if failDM && path.Base(r.URL.Path) == "sendMessage" && r.Form.Get("chat_id") == "200" {
				_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"blocked"}`))
				return
			}
		}
		writeFakeTelegramOK(w)
	})
	f := &resultFlowClient{}
	b := &Bot{api: api, client: f, logger: telegramNoopLogger(), loc: time.UTC}
	for _, id := range []int64{100, 200} {
		b.userLangCache.Store(id, userLangPref{lang: lang, hasOverride: true})
	}
	b.callbackRouter = b.buildCallbackRouter()
	wiz := &resultWizard{step: resultStepWinner, gameID: 10, opponent: oppPlayer(), gameLabel: "Game"}
	b.pendingResultWizard.Store(int64(100), wiz)
	return b, f, wiz, &texts
}
func TestResultFlow_ScoreKindSkipSubmitApprove(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.En, i18n.De, i18n.Ru} {
		for _, mode := range []string{"points", "games", "skip"} {
			t.Run(string(lang)+"/"+mode, func(t *testing.T) {
				b, f, wiz, texts := resultFlowBot(t, lang, false)
				ctx := context.Background()
				cb := callbackQuery(100, "")
				b.callbackRouter["res_winner"](ctx, cb, "me")
				if wiz.scoreKind != models.ScoreKindPoints {
					t.Fatal("points not the default")
				}
				if !strings.Contains(strings.Join(*texts, "\n"), "res_score_kind:games") {
					t.Fatal("explicit type choice missing")
				}
				if mode == "games" {
					wiz.score = "11:9"
					b.callbackRouter["res_score_kind"](ctx, cb, "games")
					if wiz.score != "" {
						t.Fatal("type change reinterpreted score")
					}
				}
				score := "11:9"
				if mode == "games" {
					score = "3:2"
				}
				if mode == "skip" {
					wiz.score = "11:9"
					b.callbackRouter["res_score_skip"](ctx, cb, "_")
					if wiz.score != "" || wiz.scoreKind != "" {
						t.Fatal("skip did not clear data")
					}
				} else {
					b.processResultWizard(ctx, &tgbotapi.Message{From: cb.From, Chat: cb.Message.Chat, Text: score}, wiz)
					if wiz.step != resultStepPreview {
						t.Fatal("score did not reach preview")
					}
					// A stale Skip keyboard must not modify the preview's selected score.
					b.callbackRouter["res_score_skip"](ctx, cb, "_")
					if wiz.score != score {
						t.Fatal("stale callback changed score")
					}
					display := resultScoreDisplay(score, wiz.scoreKind, i18n.New(lang))
					if !strings.Contains(strings.Join(*texts, "\n"), display) {
						t.Fatalf("preview missing typed score %q", display)
					}
				}
				b.callbackRouter["res_submit"](ctx, cb, "_")
				b.callbackRouter["res_submit"](ctx, cb, "_")
				if f.submitted != 1 || f.canceled != 0 || f.approvalMessage != 1 {
					t.Fatalf("submission counts: %+v", f)
				}
				if mode == "skip" {
					if f.result.Score != "" || f.result.ScoreKind != "" {
						t.Fatal("skip not transmitted")
					}
				} else if f.result.Score != score || string(f.result.ScoreKind) != mode {
					t.Fatalf("wrong submission: %+v", f.result)
				}
				b.callbackRouter["res_approve"](ctx, callbackQuery(200, ""), "77")
				if f.approved != 1 {
					t.Fatal("approval not sent")
				}
			})
		}
	}
}
func TestResultFlow_UnreachableDMCancels(t *testing.T) {
	b, f, _, _ := resultFlowBot(t, i18n.En, true)
	cb := callbackQuery(100, "")
	ctx := context.Background()
	b.handleResultPickWinner(ctx, cb, "me")
	b.handleResultScoreSkip(ctx, cb, "")
	b.handleResultSubmit(ctx, cb, "")
	if f.submitted != 1 || f.canceled != 1 || f.approvalMessage != 0 {
		t.Fatalf("unreachable DM counts: %+v", f)
	}
}
func TestResultFlow_ConcurrentScoreActions(t *testing.T) {
	// Use a separate API without a mutable recorder for concurrent requests.
	b, _, wiz, _ := resultFlowBot(t, i18n.En, false)
	b.api = newFakeBotAPI(t)
	cb := callbackQuery(100, "")
	ctx := context.Background()
	b.handleResultPickWinner(ctx, cb, "me")
	var wg sync.WaitGroup
	for _, run := range []func(){
		func() { b.handleResultScoreKind(ctx, cb, "games") },
		func() { b.handleResultScoreSkip(ctx, cb, "") },
		func() {
			b.processResultWizard(ctx, &tgbotapi.Message{From: cb.From, Chat: cb.Message.Chat, Text: "3:2"}, wiz)
		},
	} {
		wg.Add(1)
		go func(fn func()) { defer wg.Done(); fn() }(run)
	}
	wg.Wait()
	if wiz.step != resultStepPreview {
		t.Fatal("score action did not produce a preview")
	}
	if wiz.score == "" && wiz.scoreKind != "" {
		t.Fatal("inconsistent skip state")
	}
}

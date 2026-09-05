package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

type scoreAPIPlayers struct{ *apiStubPlayerRepo }

func (r scoreAPIPlayers) GetByID(_ context.Context, id int64) (*models.Player, error) {
	return &models.Player{ID: id, UserID: 43}, nil
}

type scoreAPIParts struct{ *apiStubPartRepo }

func (scoreAPIParts) GetByGame(context.Context, int64) ([]*models.GameParticipation, error) {
	return []*models.GameParticipation{
		{PlayerID: 1, Status: models.StatusRegistered}, {PlayerID: 2, Status: models.StatusRegistered},
	}, nil
}

type scoreAPIResults struct {
	*apiStubResultRepo
	created *models.GameResult
}

func (r *scoreAPIResults) Create(_ context.Context, res *models.GameResult) (int64, error) {
	r.created = res
	return 9, nil
}

func TestSubmitGameResult_ScoreContract(t *testing.T) {
	for _, tt := range []struct {
		name, fields, score string
		kind                models.ScoreKind
		status              int
	}{
		{"omitted score", "", "", "", 201},
		{"empty score", `,"score":""`, "", "", 201},
		{"legacy untyped", `,"score":"3:2"`, "3:2", "", 201},
		{"points", `,"score":"11:9","score_kind":"points"`, "11:9", models.ScoreKindPoints, 201},
		{"games", `,"score":"3:2","score_kind":"games"`, "3:2", models.ScoreKindGames, 201},
		{"unknown type", `,"score":"3:2","score_kind":"guess"`, "", "", 400},
		{"invalid equal win", `,"score":"2:2","score_kind":"games"`, "", "", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newGroupsHandler()
			rr := &scoreAPIResults{apiStubResultRepo: &apiStubResultRepo{}}
			h.gameResultSvc = service.NewGameResultService(nil, rr, &apiStubGameRepo{game: &models.Game{ID: 7, ChatID: -1, PlayersPerCourt: 2}},
				scoreAPIPlayers{&apiStubPlayerRepo{byUserID: map[int64]*models.Player{42: {ID: 1, UserID: 42}}}}, scoreAPIParts{&apiStubPartRepo{}}, service.NewAuditService(&apiStubAuditRepo{}, h.logger), 14, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader(`{"game_id":7,"author_user_id":42,"opponent_player_id":2,"winner_player_id":1`+tt.fields+`}`))
			w := httptest.NewRecorder()
			h.submitGameResult(w, req)
			if w.Code != tt.status {
				t.Fatalf("response %d: %s", w.Code, w.Body.String())
			}
			if tt.status != 201 {
				if rr.created != nil {
					t.Fatal("invalid request persisted")
				}
				return
			}
			var res models.GameResult
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if res.ID != 9 || res.Score != tt.score || res.ScoreKind != tt.kind || rr.created.ScoreKind != tt.kind {
				t.Fatalf("response %+v, persisted %+v", res, rr.created)
			}
		})
	}
}

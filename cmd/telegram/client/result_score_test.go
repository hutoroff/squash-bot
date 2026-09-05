package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/hutoroff/squash-bot/internal/models"
)

func TestSubmitGameResult_ScoreKindContract(t *testing.T) {
	for _, kind := range []models.ScoreKind{"", models.ScoreKindPoints, models.ScoreKindGames} {
		t.Run(string(kind), func(t *testing.T) {
			score := "11:9"
			if kind == "" {
				score = ""
			}
			c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assertRequest(t, r, http.MethodPost, "/api/v1/game-results")
				var body map[string]any
				decodeJSONBody(t, r, &body)
				if body["score_kind"] != string(kind) || body["score"] != score || body["author_user_id"] != float64(701) {
					t.Errorf("wrong body: %v", body)
				}
				writeJSONResponse(t, w, 201, map[string]any{"id": 1, "score": score, "score_kind": kind})
			})
			winner := int64(1)
			res, err := c.SubmitGameResult(context.Background(), 10, 701, 2, &winner, score, "author", kind)
			if err != nil || res.ScoreKind != kind || res.Score != score {
				t.Fatalf("response: %+v %v", res, err)
			}
		})
	}
}

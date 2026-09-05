package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/internal/models"
)

func TestSubmit_TypedAndSkippedScores(t *testing.T) {
	for _, tt := range []struct {
		name, score string
		kind        models.ScoreKind
		winner      *int64
		valid       bool
	}{
		{"points", "11:9", models.ScoreKindPoints, int64Ptr(1), true},
		{"games", "3:2", models.ScoreKindGames, int64Ptr(1), true},
		{"old client", "3:2", "", int64Ptr(1), true},
		{"skip", "", models.ScoreKindPoints, int64Ptr(1), true},
		{"winner identity with skip", "", "", int64Ptr(999), false},
		{"invalid kind", "11:9", "guess", int64Ptr(1), false},
		{"equal win", "2:2", models.ScoreKindGames, int64Ptr(1), false},
		{"unequal draw", "11:9", models.ScoreKindPoints, nil, false},
		{"zero draw", "0:0", models.ScoreKindPoints, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr, gr, pr, pp := defaultFixture()
			svc := newResultSvc(rr, gr, pr, pp)
			res, err := svc.Submit(context.Background(), 10, 100, 2, tt.winner, tt.score, "author", tt.kind)
			if !tt.valid {
				if !errors.Is(err, ErrGameResultBadScore) || len(rr.created) != 0 {
					t.Fatalf("invalid accepted: %+v %v", res, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantKind := tt.kind
			if tt.score == "" {
				wantKind = ""
			}
			if res.Score != tt.score || res.ScoreKind != wantKind {
				t.Fatalf("result: %+v", res)
			}
		})
	}
}

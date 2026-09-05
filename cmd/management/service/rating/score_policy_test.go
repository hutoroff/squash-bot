package rating

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/hutoroff/squash-bot/internal/models"
)

func TestScorePolicy(t *testing.T) {
	winner := int64(1)
	for _, tt := range []struct {
		name, score string
		kind        models.ScoreKind
		winner      *int64
		enabled     bool
		weight      float64
		reason      string
	}{
		{"off", "11:0", models.ScoreKindPoints, &winner, false, 1, "disabled"},
		{"skipped", "", "", &winner, true, 1, "missing_score"},
		{"unknown", "3:1", "", &winner, true, 1, "unknown_score_kind"},
		{"close points", "11:9", models.ScoreKindPoints, &winner, true, .8, "typed_score"},
		{"decisive points", "11:0", models.ScoreKindPoints, &winner, true, 1.25, "typed_score"},
		{"games", "3:2", models.ScoreKindGames, &winner, true, .85, "typed_score"},
		{"draw", "2:2", models.ScoreKindGames, nil, true, 1, "draw"},
		{"zero draw", "0:0", models.ScoreKindPoints, nil, true, 1, "draw"},
		{"legacy zero win", "0:0", models.ScoreKindPoints, &winner, true, 1, "invalid_legacy_score"},
		{"legacy invalid", "3:2", models.ScoreKindGames, nil, true, 1, "invalid_legacy_score"},
		{"legacy overflow", "9999999999999999999999:0", models.ScoreKindPoints, &winner, true, 1, "invalid_legacy_score"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := SelectScorePolicy(&models.GameResult{AuthorID: 1, OpponentID: 2, WinnerID: tt.winner, Score: tt.score, ScoreKind: tt.kind}, tt.enabled)
			if math.Abs(p.Weight-tt.weight) > 1e-12 || p.Reason != tt.reason {
				t.Fatalf("policy = %+v", p)
			}
			if (p.Version == "glicko2-score-v1") != (tt.reason == "typed_score") {
				t.Fatalf("wrong version: %+v", p)
			}
		})
	}
}

func TestWeightedGlicko_LegacyExactAndDirection(t *testing.T) {
	for _, rd := range []float64{30, 100, 350} {
		for _, oppRD := range []float64{30, 100, 350} {
			for _, gap := range []float64{-1000, -400, 0, 400, 1000} {
				p := Rating{1500 + gap, rd, .06}
				opp := Rating{1500, oppRD, .06}
				for _, outcome := range []float64{0, .5, 1} {
					legacy := Apply(p, []MatchResult{{Opponent: opp, Score: outcome}}, Tau)
					explicit := Apply(p, []MatchResult{{Opponent: opp, Score: outcome, Weight: 1}}, Tau)
					if legacy != explicit {
						t.Fatal("weight 1 differs from standard path")
					}
					if outcome == .5 {
						continue
					}
					previous := 0.0
					for _, weight := range []float64{.75, .8, 1, 1.25} {
						next := Apply(p, []MatchResult{{Opponent: opp, Score: outcome, Weight: weight}}, Tau)
						if math.IsNaN(next.R) || math.IsInf(next.R, 0) || math.IsNaN(next.RD) || math.IsNaN(next.Sigma) || next.RD < 30 || next.RD > 350 || next.Sigma <= 0 {
							t.Fatalf("invalid state: %+v", next)
						}
						delta := next.R - p.R
						if (outcome == 1 && delta <= 0) || (outcome == 0 && delta >= 0) {
							t.Fatalf("direction reversed: %v -> %v", p, next)
						}
						if math.Abs(delta) < previous {
							t.Fatalf("margin not monotonic: gap=%v RD=%v oppRD=%v weight=%v", gap, rd, oppRD, weight)
						}
						previous = math.Abs(delta)
					}
				}
			}
		}
	}
}

// A reproducible stress simulation, NOT proof of predictive improvement. Score
// margins correlate with the stronger player's wins, deliberately exposing the
// bias risk of outcome-dependent weighting. Keep experimental flags off by default.
func TestWeightedGlicko_SyntheticSimulation(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 31))
	var ordinary, weighted [8]Rating
	for i := range ordinary {
		ordinary[i] = Default()
		weighted[i] = Default()
	}
	for n := 0; n < 10000; n++ {
		a := rng.IntN(8)
		b := (a + 1 + rng.IntN(7)) % 8
		gap := float64(a-b) * 80
		probability := 1 / (1 + math.Exp(-gap/scaleToGlicko2))
		outcome := 0.0
		if rng.Float64() < probability {
			outcome = 1
		}
		// Under this toy score model favorites win more decisively than underdogs.
		margin := .1
		if (gap > 0 && outcome == 1) || (gap < 0 && outcome == 0) {
			margin = .6
		}
		for mode, states := range []*[8]Rating{&ordinary, &weighted} {
			weight := 1.0
			if mode == 1 {
				weight = .75 + .5*margin
			}
			oldA, oldB := states[a], states[b]
			states[a] = Apply(oldA, []MatchResult{{Opponent: oldB, Score: outcome, Weight: weight}}, Tau)
			states[b] = Apply(oldB, []MatchResult{{Opponent: oldA, Score: 1 - outcome, Weight: weight}}, Tau)
			for _, p := range []Rating{states[a], states[b]} {
				if math.IsNaN(p.R) || math.IsInf(p.R, 0) || p.RD < 30 || p.RD > 350 || math.IsNaN(p.Sigma) || math.IsInf(p.Sigma, 0) {
					t.Fatalf("nonfinite/unbounded state at match %d: %+v", n, p)
				}
			}
		}
	}
	for i := range ordinary {
		t.Logf("player %d: ordinary %.1f; weighted %.1f; difference %+.1f", i, ordinary[i].R, weighted[i].R, weighted[i].R-ordinary[i].R)
	}
}

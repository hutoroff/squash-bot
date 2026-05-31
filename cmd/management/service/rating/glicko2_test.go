package rating

import (
	"math"
	"testing"
)

// TestApply_GlickmanPaperExample verifies against the worked example in
// Glickman (2013): player r=1500, RD=200, σ=0.06 versus three opponents.
// Expected: r'≈1464.06, RD'≈151.52, σ'≈0.059996 (paper rounds to 0.06).
func TestApply_GlickmanPaperExample(t *testing.T) {
	p := Rating{R: 1500, RD: 200, Sigma: 0.06}
	results := []MatchResult{
		{Opponent: Rating{R: 1400, RD: 30, Sigma: 0.06}, Score: 1},
		{Opponent: Rating{R: 1550, RD: 100, Sigma: 0.06}, Score: 0},
		{Opponent: Rating{R: 1700, RD: 300, Sigma: 0.06}, Score: 0},
	}
	got := Apply(p, results, Tau)

	if math.Abs(got.R-1464.06) > 1.0 {
		t.Errorf("R: want ≈1464.06, got %.4f", got.R)
	}
	if math.Abs(got.RD-151.52) > 1.0 {
		t.Errorf("RD: want ≈151.52, got %.4f", got.RD)
	}
}

func TestApply_Draw(t *testing.T) {
	p := Rating{R: 1500, RD: 200, Sigma: 0.06}
	opp := Rating{R: 1500, RD: 200, Sigma: 0.06}
	got := Apply(p, []MatchResult{{Opponent: opp, Score: 0.5}}, Tau)
	// A draw against an equal opponent should leave rating roughly unchanged.
	if math.Abs(got.R-p.R) > 5 {
		t.Errorf("draw: rating changed too much: %.4f", got.R)
	}
}

func TestApply_NoMatches(t *testing.T) {
	p := Rating{R: 1500, RD: 200, Sigma: 0.06}
	got := Apply(p, nil, Tau)
	if got.R != p.R {
		t.Errorf("no matches: R should not change, got %.4f", got.R)
	}
	if got.RD <= p.RD {
		t.Errorf("no matches: RD should increase, got %.4f (was %.4f)", got.RD, p.RD)
	}
}

func TestApply_SingleWin(t *testing.T) {
	p := Rating{R: 1500, RD: 350, Sigma: 0.06}
	opp := Rating{R: 1500, RD: 350, Sigma: 0.06}
	got := Apply(p, []MatchResult{{Opponent: opp, Score: 1}}, Tau)
	if got.R <= p.R {
		t.Errorf("single win: rating should increase, got %.4f (was %.4f)", got.R, p.R)
	}
}

func TestApply_RDDecreasesWithGames(t *testing.T) {
	p := Rating{R: 1500, RD: 350, Sigma: 0.06}
	opp := Rating{R: 1500, RD: 200, Sigma: 0.06}
	got := Apply(p, []MatchResult{{Opponent: opp, Score: 0.5}}, Tau)
	if got.RD >= p.RD {
		t.Errorf("RD should decrease after a game, got %.4f (was %.4f)", got.RD, p.RD)
	}
}

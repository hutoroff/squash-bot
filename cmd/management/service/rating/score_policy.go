package rating

import (
	"math"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/resultscore"
)

type ScorePolicy struct {
	Version string
	Weight  float64
	Reason  string
}

// SelectScorePolicy never infers score kind. Invalid legacy scores and unknown
// kinds remain rateable with standard Glicko-2. New submissions are validated
// separately. This experimental power-likelihood weighting is not standard Glicko-2.
func SelectScorePolicy(result *models.GameResult, enabled bool) ScorePolicy {
	p := ScorePolicy{Version: "glicko2-v1", Weight: 1, Reason: "disabled"}
	if !enabled {
		return p
	}
	if result.Score == "" {
		p.Reason = "missing_score"
		return p
	}
	if !result.ScoreKind.Valid() {
		p.Reason = "unknown_score_kind"
		return p
	}
	if resultscore.Validate(result.Score, result.AuthorID, result.OpponentID, result.WinnerID) != nil {
		p.Reason = "invalid_legacy_score"
		return p
	}
	if result.WinnerID == nil {
		p.Reason = "draw"
		return p
	}
	a, b, _ := resultscore.Parse(result.Score)
	margin := math.Abs(float64(a)-float64(b)) / (float64(a) + float64(b))
	return ScorePolicy{Version: "glicko2-score-v1", Weight: 0.75 + 0.5*margin, Reason: "typed_score"}
}

// Package resultscore defines transport-independent validation for optional N:M scores.
package resultscore

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("invalid score: use non-negative N:M; winner must have the higher score, draw must have equal scores")

// Parse bounds each side to six decimal digits (0..999999). This is an input
// safety limit, not an attempt to enforce any sport's official scoring rules.
func Parse(score string) (int, int, error) {
	left, right, ok := strings.Cut(score, ":")
	if !ok {
		return 0, 0, ErrInvalid
	}
	parse := func(s string) (int, error) {
		if len(s) == 0 || len(s) > 6 {
			return 0, ErrInvalid
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, ErrInvalid
			}
		}
		return strconv.Atoi(s)
	}
	a, err := parse(left)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	b, err := parse(right)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	return a, b, nil
}

// Validate always checks winner identity, even when the score was skipped.
func Validate(score string, author, opponent int64, winner *int64) error {
	if winner != nil && *winner != author && *winner != opponent {
		return ErrInvalid
	}
	if score == "" {
		return nil
	}
	a, b, err := Parse(score)
	if err != nil {
		return err
	}
	if winner == nil {
		if a != b {
			return ErrInvalid
		}
	} else if (*winner == author && a <= b) || (*winner == opponent && b <= a) {
		return ErrInvalid
	}
	return nil
}

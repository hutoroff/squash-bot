package service

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
)

// TestProcessDayAfter_NilMessageID is a regression test for the panic that occurred
// when processDayAfter unconditionally dereferenced game.MessageID without a nil
// check. The function must return early without panicking when MessageID is nil.
// Passing a nil s.api proves that the Telegram API is never reached.
func TestProcessDayAfter_NilMessageID(t *testing.T) {
	// s.api is intentionally nil — if the nil-guard is missing the function would
	// panic on *game.MessageID before ever reaching the API, but having api=nil
	// ensures any accidental API call also panics, making the test self-validating.
	s := &DayAfterCleanupJob{logger: noopLogger()}

	game := &models.Game{
		ID:        42,
		ChatID:    -1001,
		MessageID: nil, // the problematic case
	}

	// Must not panic.
	s.processDayAfter(context.Background(), game, time.UTC)
}

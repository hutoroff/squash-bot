package outbound

import "context"

// Notifier edits the Telegram group message for a game to reflect current participation state.
type Notifier interface {
	EditGameMessage(ctx context.Context, gameID int64)
}

package inbound

import "context"

// AdminGroupsResolver resolves which groups a Telegram user administers.
type AdminGroupsResolver interface {
	AdminGroupsFor(ctx context.Context, tgID int64) ([]int64, error)
}

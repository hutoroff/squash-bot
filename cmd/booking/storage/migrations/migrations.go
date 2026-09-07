package migrations

import "embed"

// FS contains the booking-owned migration stream. It deliberately uses a
// separate history table from management's root migrations.
//
//go:embed *.sql
var FS embed.FS

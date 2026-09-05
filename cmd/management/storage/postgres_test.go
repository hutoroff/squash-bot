//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Compatibility coverage for driver upgrades: keep numbers and quote/comment-
// shaped text as data in both the extended- and simple-protocol query paths.
// This does not attempt a multi-gigabyte protocol-overflow exploit.
func TestPostgresParameterBoundaries(t *testing.T) {
	for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement, pgx.QueryExecModeSimpleProtocol} {
		t.Run(mode.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			const text = "O'Reilly\\text\n-- still parameter data"
			var number int32
			var echoed string
			err := testPool.QueryRow(ctx, "SELECT -$1::int, $2::text", mode, int32(-1), text).Scan(&number, &echoed)
			if err != nil {
				t.Fatalf("query bound parameters: %v", err)
			}
			if number != 1 || echoed != text {
				t.Fatalf("parameters changed query semantics: got (%d, %q), want (1, %q)", number, echoed, text)
			}
		})
	}
}

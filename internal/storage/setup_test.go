//go:build integration

package storage_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

// testPool is shared across all storage integration tests in this package.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	if !testutil.IsDockerAvailable() {
		fmt.Fprintln(os.Stderr, "Docker not available — skipping storage integration tests")
		os.Exit(0)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		log.Fatalf("setup test db: %v", err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

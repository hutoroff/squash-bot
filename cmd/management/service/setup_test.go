//go:build integration

package service_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool is shared across all service integration tests in this package.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	if err := testutil.CheckDocker(); err != nil {
		fmt.Fprintf(os.Stderr, "service integration tests require Docker: %v\n", err)
		os.Exit(1)
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

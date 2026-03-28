//go:build e2e

// Package e2e contains end-to-end tests that start a real Docker environment.
//
// Run with:
//
//	go test -v -tags e2e ./tests/e2e/...
//
// Requires Docker and docker-compose (or the docker compose plugin) to be available.
package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

const (
	testDSN            = "postgres://squash_test:squash_test@localhost:8433/squash_test?sslmode=disable"
	composeFile        = "../../docker-compose.test.yml"
	composeServiceName = "postgres-test"
	startupTimeout     = 60 * time.Second
	connectRetryDelay  = 500 * time.Millisecond
)

// dockerComposeCmd returns an exec.Cmd for either "docker-compose" or "docker compose".
func dockerComposeCmd(args ...string) *exec.Cmd {
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return exec.Command("docker-compose", append([]string{"-f", composeFile}, args...)...)
	}
	return exec.Command("docker", append([]string{"compose", "-f", composeFile}, args...)...)
}

// waitForPostgres retries connecting to the test database until it is ready or the timeout expires.
func waitForPostgres(ctx context.Context, dsn string, timeout time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			}
			pool.Close()
		}
		time.Sleep(connectRetryDelay)
	}
	return nil, fmt.Errorf("postgres not ready after %s", timeout)
}

// TestEnvironmentStartup verifies that:
//  1. docker-compose.test.yml starts a healthy PostgreSQL instance
//  2. Database migrations run successfully against it
//  3. All application layers (storage + service) work correctly end-to-end
func TestEnvironmentStartup(t *testing.T) {
	// Verify Docker is available
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available, skipping e2e test")
	}

	ctx := context.Background()

	// --- Start environment ---
	upCmd := dockerComposeCmd("up", "-d", "--wait")
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		// Try without --wait (older compose versions)
		upCmd = dockerComposeCmd("up", "-d")
		upCmd.Stdout = os.Stdout
		upCmd.Stderr = os.Stderr
		if err2 := upCmd.Run(); err2 != nil {
			t.Fatalf("docker-compose up: %v", err2)
		}
	}

	t.Cleanup(func() {
		downCmd := dockerComposeCmd("down", "-v")
		downCmd.Stdout = os.Stdout
		downCmd.Stderr = os.Stderr
		if err := downCmd.Run(); err != nil {
			t.Logf("docker-compose down: %v", err)
		}
	})

	// --- Wait for PostgreSQL to be ready ---
	t.Log("Waiting for PostgreSQL to be ready...")
	pool, err := waitForPostgres(ctx, testDSN, startupTimeout)
	if err != nil {
		t.Fatalf("postgres never became ready: %v", err)
	}
	defer pool.Close()
	t.Log("PostgreSQL is ready")

	// --- Run migrations ---
	if err := testutil.RunMigrations(testDSN); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	t.Log("Migrations applied")

	// --- Verify schema: all expected tables exist ---
	t.Run("schema_tables_exist", func(t *testing.T) {
		for _, table := range []string{"games", "players", "game_participations", "guest_participations"} {
			var exists bool
			err := pool.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table,
			).Scan(&exists)
			if err != nil {
				t.Fatalf("check table %q: %v", table, err)
			}
			if !exists {
				t.Errorf("table %q does not exist after migrations", table)
			}
		}
	})

	// --- Full game lifecycle via application services ---
	t.Run("full_game_lifecycle", func(t *testing.T) {
		if err := testutil.Truncate(ctx, pool); err != nil {
			t.Fatal(err)
		}

		gameRepo := storage.NewGameRepo(pool)
		playerRepo := storage.NewPlayerRepo(pool)
		partRepo := storage.NewParticipationRepo(pool)
		guestRepo := storage.NewGuestRepo(pool)
		gameSvc := service.NewGameService(gameRepo)
		partSvc := service.NewParticipationService(playerRepo, partRepo, guestRepo)

		// 1. Create a game (2 courts → capacity 4)
		gameDate := time.Now().Add(48 * time.Hour)
		game, err := gameSvc.CreateGame(ctx, -9001, gameDate, "1,2")
		if err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		if game.ID == 0 {
			t.Fatal("game ID should be non-zero")
		}

		// 2. Set message ID (simulates bot pinning the announcement)
		if err := gameSvc.UpdateMessageID(ctx, game.ID, 555); err != nil {
			t.Fatalf("UpdateMessageID: %v", err)
		}
		game, _ = gameSvc.GetByID(ctx, game.ID)
		if game.MessageID == nil || *game.MessageID != 555 {
			t.Fatalf("MessageID not set correctly: %v", game.MessageID)
		}

		// 3. Two players join
		parts, err := partSvc.Join(ctx, game.ID, 3001, "alice", "Alice", "")
		if err != nil || len(parts) != 1 {
			t.Fatalf("alice join: err=%v, parts=%d", err, len(parts))
		}
		parts, err = partSvc.Join(ctx, game.ID, 3002, "bob", "Bob", "")
		if err != nil || len(parts) != 2 {
			t.Fatalf("bob join: err=%v, parts=%d", err, len(parts))
		}

		// 4. Verify registered count
		count, err := partRepo.GetRegisteredCount(ctx, game.ID)
		if err != nil || count != 2 {
			t.Fatalf("registered count: got %d, want 2 (err: %v)", count, err)
		}

		// 5. Alice skips
		parts, skipped, err := partSvc.Skip(ctx, game.ID, 3001, "alice", "Alice", "")
		if err != nil {
			t.Fatalf("alice skip: %v", err)
		}
		if !skipped {
			t.Error("expected skipped=true for registered alice")
		}
		count, _ = partRepo.GetRegisteredCount(ctx, game.ID)
		if count != 1 {
			t.Errorf("registered count after alice skip: got %d, want 1", count)
		}
		_ = parts

		// 6. Alice rejoins
		_, err = partSvc.Join(ctx, game.ID, 3001, "alice", "Alice", "")
		if err != nil {
			t.Fatalf("alice rejoin: %v", err)
		}
		count, _ = partRepo.GetRegisteredCount(ctx, game.ID)
		if count != 2 {
			t.Errorf("registered count after alice rejoin: got %d, want 2", count)
		}

		// 7. Day-before: game should appear in tomorrow's window (not notified)
		tomorrow := time.Now().Add(24 * time.Hour)
		dayFrom := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, time.UTC)
		dayTo := dayFrom.AddDate(0, 0, 1)
		dayBeforeGames, err := gameRepo.GetGamesForDayBefore(ctx,
			dayFrom.Add(-24*time.Hour),
			dayTo.Add(48*time.Hour),
		)
		if err != nil {
			t.Fatalf("GetGamesForDayBefore: %v", err)
		}
		found := false
		for _, g := range dayBeforeGames {
			if g.ID == game.ID {
				found = true
				break
			}
		}
		if !found {
			t.Error("game should appear in day-before window before being notified")
		}

		// 8. Mark day-before notified
		if err := gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
			t.Fatalf("MarkNotifiedDayBefore: %v", err)
		}
		// Should no longer appear in day-before query
		dayBeforeGames, _ = gameRepo.GetGamesForDayBefore(ctx,
			dayFrom.Add(-24*time.Hour),
			dayTo.Add(48*time.Hour),
		)
		for _, g := range dayBeforeGames {
			if g.ID == game.ID {
				t.Error("game should NOT appear in day-before window after being notified")
			}
		}

		// 9. Day-after cleanup: verify query with completed=false and message_id set
		yesterday := time.Now().Add(-24 * time.Hour)
		pastGame, _ := gameSvc.CreateGame(ctx, -9001, yesterday, "3")
		_ = gameSvc.UpdateMessageID(ctx, pastGame.ID, 777)

		pastFrom := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
		pastTo := pastFrom.AddDate(0, 0, 1)
		dayAfterGames, err := gameRepo.GetGamesForDayAfter(ctx, pastFrom, pastTo)
		if err != nil {
			t.Fatalf("GetGamesForDayAfter: %v", err)
		}
		foundPast := false
		for _, g := range dayAfterGames {
			if g.ID == pastGame.ID {
				foundPast = true
				break
			}
		}
		if !foundPast {
			t.Error("past game with message_id should appear in day-after window")
		}

		// 10. Mark past game completed
		if err := gameRepo.MarkCompleted(ctx, pastGame.ID); err != nil {
			t.Fatalf("MarkCompleted: %v", err)
		}
		dayAfterGames, _ = gameRepo.GetGamesForDayAfter(ctx, pastFrom, pastTo)
		for _, g := range dayAfterGames {
			if g.ID == pastGame.ID {
				t.Error("completed game should NOT appear in day-after window")
			}
		}

		// 11. Verify upcoming games list
		upcoming, err := gameRepo.GetUpcomingGames(ctx)
		if err != nil {
			t.Fatalf("GetUpcomingGames: %v", err)
		}
		foundUpcoming := false
		for _, g := range upcoming {
			if g.ID == game.ID {
				foundUpcoming = true
			}
			if g.ID == pastGame.ID {
				t.Error("completed/past game should not be in upcoming list")
			}
		}
		if !foundUpcoming {
			t.Error("active future game should appear in upcoming list")
		}

		// 12. Verify final participation state for the active game
		allParts, err := partRepo.GetByGame(ctx, game.ID)
		if err != nil {
			t.Fatalf("GetByGame: %v", err)
		}
		registeredCount := 0
		for _, p := range allParts {
			if p.Status == models.StatusRegistered {
				registeredCount++
			}
			if p.Player == nil {
				t.Error("player data should be denormalized in participations")
			}
		}
		if registeredCount != 2 {
			t.Errorf("final registered count: got %d, want 2", registeredCount)
		}
	})

	t.Log("All e2e checks passed")
}

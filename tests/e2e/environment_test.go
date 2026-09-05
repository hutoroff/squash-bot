//go:build e2e

// Package e2e contains the service/database lifecycle test. The historical
// build tag is retained for command compatibility; this is not a browser,
// HTTP transport, Telegram, or external-booking end-to-end suite.
//
// Run with:
//
//	go test -count=1 -tags e2e -timeout 120s ./tests/e2e/...
//
// Docker is required. PostgreSQL runs in a disposable Testcontainers container.
package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

// TestServiceDatabaseLifecycle verifies migrations and a representative game
// lifecycle through current management service and storage contracts.
func TestServiceDatabaseLifecycle(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("service/database lifecycle test requires Docker: %v", err)
	}

	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatalf("setup test database: %v", err)
	}
	t.Cleanup(cleanup)

	t.Run("schema migrations", func(t *testing.T) {
		for _, table := range []string{
			"users", "user_identities", "players", "games",
			"game_participations", "guest_participations", "bot_groups", "venues",
		} {
			var exists bool
			if err := pool.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)",
				table,
			).Scan(&exists); err != nil {
				t.Fatalf("check table %q: %v", table, err)
			}
			if !exists {
				t.Errorf("table %q does not exist after migrations", table)
			}
		}
	})

	t.Run("game and participation lifecycle", func(t *testing.T) {
		if err := testutil.Truncate(ctx, pool); err != nil {
			t.Fatal(err)
		}

		gameRepo := storage.NewGameRepo(pool)
		partRepo := storage.NewParticipationRepo(pool)
		gameSvc := service.NewGameService(
			gameRepo,
			storage.NewVenueRepo(pool),
			partRepo,
			storage.NewGuestRepo(pool),
			nil, nil, nil, time.UTC, nil, nil, nil, nil, nil, 14,
		)
		partSvc := service.NewParticipationService(
			storage.NewPlayerRepo(pool),
			partRepo,
			storage.NewGuestRepo(pool),
			nil,
		)

		aliceUserID, err := testutil.CreateTestUser(ctx, pool, 3001, "alice")
		if err != nil {
			t.Fatalf("create Alice identity: %v", err)
		}
		bobUserID, err := testutil.CreateTestUser(ctx, pool, 3002, "bob")
		if err != nil {
			t.Fatalf("create Bob identity: %v", err)
		}

		gameDate := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
		game, err := gameSvc.CreateGame(ctx, -9001, gameDate, "1,2", nil)
		if err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		if game.ID == 0 || game.Capacity() != 4 {
			t.Fatalf("created game has ID %d and capacity %d; want non-zero ID and capacity 4", game.ID, game.Capacity())
		}

		if err := gameSvc.UpdateMessageID(ctx, game.ID, 555); err != nil {
			t.Fatalf("UpdateMessageID: %v", err)
		}
		game, err = gameSvc.GetByID(ctx, game.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if game.MessageID == nil || *game.MessageID != 555 {
			t.Fatalf("MessageID = %v, want 555", game.MessageID)
		}

		parts, err := partSvc.Join(ctx, game.ID, aliceUserID)
		if err != nil || len(parts) != 1 {
			t.Fatalf("Alice Join: err=%v, participations=%d", err, len(parts))
		}
		parts, err = partSvc.Join(ctx, game.ID, bobUserID)
		if err != nil || len(parts) != 2 {
			t.Fatalf("Bob Join: err=%v, participations=%d", err, len(parts))
		}

		parts, skipped, err := partSvc.Skip(ctx, game.ID, aliceUserID)
		if err != nil || !skipped {
			t.Fatalf("Alice Skip: err=%v, skipped=%v", err, skipped)
		}
		if len(parts) != 2 {
			t.Fatalf("participations after skip = %d, want 2", len(parts))
		}
		if _, err := partSvc.Join(ctx, game.ID, aliceUserID); err != nil {
			t.Fatalf("Alice rejoin: %v", err)
		}
		if count, err := partRepo.GetRegisteredCount(ctx, game.ID); err != nil || count != 2 {
			t.Fatalf("registered count = %d, want 2 (err: %v)", count, err)
		}

		windowStart := gameDate.Add(-time.Minute)
		windowEnd := gameDate.Add(time.Minute)
		dayBeforeGames, err := gameRepo.GetGamesForDayBefore(ctx, windowStart, windowEnd)
		if err != nil || !containsGame(dayBeforeGames, game.ID) {
			t.Fatalf("unnotified game missing from day-before query: err=%v", err)
		}
		if err := gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
			t.Fatalf("MarkNotifiedDayBefore: %v", err)
		}
		dayBeforeGames, err = gameRepo.GetGamesForDayBefore(ctx, windowStart, windowEnd)
		if err != nil {
			t.Fatalf("second day-before query: %v", err)
		}
		if containsGame(dayBeforeGames, game.ID) {
			t.Error("notified game still appears in day-before query")
		}

		pastDate := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
		pastGame, err := gameSvc.CreateGame(ctx, -9001, pastDate, "3", nil)
		if err != nil {
			t.Fatalf("create past game: %v", err)
		}
		if err := gameSvc.UpdateMessageID(ctx, pastGame.ID, 777); err != nil {
			t.Fatalf("set past game message: %v", err)
		}
		dayAfterGames, err := gameRepo.GetGamesForDayAfter(ctx, pastDate.Add(-time.Minute), pastDate.Add(time.Minute))
		if err != nil || !containsGame(dayAfterGames, pastGame.ID) {
			t.Fatalf("past game missing from day-after query: err=%v", err)
		}
		if err := gameRepo.MarkCompleted(ctx, pastGame.ID); err != nil {
			t.Fatalf("MarkCompleted: %v", err)
		}
		dayAfterGames, err = gameRepo.GetGamesForDayAfter(ctx, pastDate.Add(-time.Minute), pastDate.Add(time.Minute))
		if err != nil {
			t.Fatalf("second day-after query: %v", err)
		}
		if containsGame(dayAfterGames, pastGame.ID) {
			t.Error("completed game still appears in day-after query")
		}

		upcoming, err := gameRepo.GetUpcomingGames(ctx)
		if err != nil {
			t.Fatalf("GetUpcomingGames: %v", err)
		}
		if !containsGame(upcoming, game.ID) || containsGame(upcoming, pastGame.ID) {
			t.Errorf("upcoming games do not contain only the active future game")
		}

		finalParts, err := partRepo.GetByGame(ctx, game.ID)
		if err != nil {
			t.Fatalf("GetByGame: %v", err)
		}
		for _, part := range finalParts {
			if part.Status != models.StatusRegistered {
				t.Errorf("participation %d status = %q, want registered", part.ID, part.Status)
			}
			if part.Player == nil || part.Player.UserID == 0 {
				t.Errorf("participation %d has no canonical player identity", part.ID)
			}
		}
	})
}

func containsGame(games []*models.Game, gameID int64) bool {
	for _, game := range games {
		if game.ID == gameID {
			return true
		}
	}
	return false
}

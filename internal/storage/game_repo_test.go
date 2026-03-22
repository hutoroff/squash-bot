//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

func mustTruncate(t *testing.T) {
	t.Helper()
	if err := testutil.Truncate(context.Background(), testPool); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

func newGame(chatID int64, date time.Time, courts string) *models.Game {
	return &models.Game{
		ChatID:      chatID,
		GameDate:    date,
		Courts:      courts,
		CourtsCount: len(splitCourts(courts)),
	}
}

func splitCourts(courts string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(courts); i++ {
		if i == len(courts) || courts[i] == ',' {
			parts = append(parts, courts[start:i])
			start = i + 1
		}
	}
	return parts
}

func TestGameRepo_Create(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	gameDate := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	got, err := repo.Create(ctx, newGame(-1001, gameDate, "1,2,3"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got.ID == 0 {
		t.Error("expected non-zero ID after Create")
	}
	if got.ChatID != -1001 {
		t.Errorf("ChatID: got %d, want -1001", got.ChatID)
	}
	if got.Courts != "1,2,3" {
		t.Errorf("Courts: got %q, want %q", got.Courts, "1,2,3")
	}
	if got.CourtsCount != 3 {
		t.Errorf("CourtsCount: got %d, want 3", got.CourtsCount)
	}
	if got.MessageID != nil {
		t.Error("new game should have nil MessageID")
	}
	if got.Completed {
		t.Error("new game should not be completed")
	}
	if got.NotifiedDayBefore {
		t.Error("new game should not be notified_day_before")
	}
}

func TestGameRepo_GetByID(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	created, _ := repo.Create(ctx, newGame(-1002, time.Now().Add(24*time.Hour), "5,6"))

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %d, want %d", got.ID, created.ID)
	}
	if got.Courts != "5,6" {
		t.Errorf("Courts: got %q, want %q", got.Courts, "5,6")
	}
}

func TestGameRepo_GetUpcomingGames(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	future := time.Now().Add(72 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	g1, _ := repo.Create(ctx, newGame(-1, future, "1"))
	g2, _ := repo.Create(ctx, newGame(-1, future, "2"))
	pastGame, _ := repo.Create(ctx, newGame(-1, past, "3"))

	// mark one future game as completed
	_ = repo.MarkCompleted(ctx, g2.ID)
	// past game should also not appear (game_date < now)
	_ = pastGame

	games, err := repo.GetUpcomingGames(ctx)
	if err != nil {
		t.Fatalf("GetUpcomingGames: %v", err)
	}

	if len(games) != 1 {
		t.Fatalf("got %d upcoming games, want 1", len(games))
	}
	if games[0].ID != g1.ID {
		t.Errorf("unexpected game ID: got %d, want %d", games[0].ID, g1.ID)
	}
}

func TestGameRepo_UpdateMessageID(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	g, _ := repo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1"))

	if err := repo.UpdateMessageID(ctx, g.ID, 999); err != nil {
		t.Fatalf("UpdateMessageID: %v", err)
	}

	updated, _ := repo.GetByID(ctx, g.ID)
	if updated.MessageID == nil {
		t.Fatal("MessageID is nil after update")
	}
	if *updated.MessageID != 999 {
		t.Errorf("MessageID: got %d, want 999", *updated.MessageID)
	}
}

func TestGameRepo_GetGamesForDayBefore(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	tomorrow := time.Now().Add(24 * time.Hour)
	from := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)

	// game tomorrow, not notified – should appear
	g1, _ := repo.Create(ctx, newGame(-1, from.Add(18*time.Hour), "1"))
	// game tomorrow, already notified – should NOT appear
	g2, _ := repo.Create(ctx, newGame(-1, from.Add(20*time.Hour), "2"))
	_ = repo.MarkNotifiedDayBefore(ctx, g2.ID)
	// game day after tomorrow – should NOT appear
	_, _ = repo.Create(ctx, newGame(-1, to.Add(24*time.Hour), "3"))

	games, err := repo.GetGamesForDayBefore(ctx, from, to)
	if err != nil {
		t.Fatalf("GetGamesForDayBefore: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1", len(games))
	}
	if games[0].ID != g1.ID {
		t.Errorf("unexpected game: got ID %d, want %d", games[0].ID, g1.ID)
	}
}

func TestGameRepo_MarkNotifiedDayBefore(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	g, _ := repo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1"))
	if err := repo.MarkNotifiedDayBefore(ctx, g.ID); err != nil {
		t.Fatalf("MarkNotifiedDayBefore: %v", err)
	}

	updated, _ := repo.GetByID(ctx, g.ID)
	if !updated.NotifiedDayBefore {
		t.Error("NotifiedDayBefore should be true after marking")
	}
}

func TestGameRepo_GetGamesForDayAfter(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	yesterday := time.Now().Add(-24 * time.Hour)
	from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)

	msgID := int64(42)

	// game yesterday with message_id, not completed – should appear
	g1, _ := repo.Create(ctx, newGame(-1, from.Add(18*time.Hour), "1"))
	_ = repo.UpdateMessageID(ctx, g1.ID, msgID)

	// game yesterday, no message_id – should NOT appear
	_, _ = repo.Create(ctx, newGame(-1, from.Add(19*time.Hour), "2"))

	// game yesterday with message_id, already completed – should NOT appear
	g3, _ := repo.Create(ctx, newGame(-1, from.Add(20*time.Hour), "3"))
	_ = repo.UpdateMessageID(ctx, g3.ID, msgID)
	_ = repo.MarkCompleted(ctx, g3.ID)

	games, err := repo.GetGamesForDayAfter(ctx, from, to)
	if err != nil {
		t.Fatalf("GetGamesForDayAfter: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1", len(games))
	}
	if games[0].ID != g1.ID {
		t.Errorf("unexpected game: got ID %d, want %d", games[0].ID, g1.ID)
	}
}

func TestGameRepo_MarkCompleted(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	g, _ := repo.Create(ctx, newGame(-1, time.Now().Add(-24*time.Hour), "1"))
	if err := repo.MarkCompleted(ctx, g.ID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	updated, _ := repo.GetByID(ctx, g.ID)
	if !updated.Completed {
		t.Error("Completed should be true after marking")
	}
}

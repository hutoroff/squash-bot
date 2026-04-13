//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
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

// TestGameRepo_GetGamesForDayBefore_ExcludesCompleted is a regression test for the
// bug where GetGamesForDayBefore was missing "AND completed = false", causing the
// day-before notification to fire for games that were already completed (e.g. via
// a manual day_after trigger).
func TestGameRepo_GetGamesForDayBefore_ExcludesCompleted(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	tomorrow := time.Now().Add(24 * time.Hour)
	from := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)

	// Active game: should appear in results.
	active, _ := repo.Create(ctx, newGame(-1, from.Add(18*time.Hour), "1"))

	// Completed game in same time range: must NOT appear even though notified_day_before is false.
	completed, _ := repo.Create(ctx, newGame(-1, from.Add(19*time.Hour), "2"))
	_ = repo.MarkCompleted(ctx, completed.ID)

	games, err := repo.GetGamesForDayBefore(ctx, from, to)
	if err != nil {
		t.Fatalf("GetGamesForDayBefore: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1 (completed game must be excluded)", len(games))
	}
	if games[0].ID != active.ID {
		t.Errorf("got game ID %d, want active game ID %d", games[0].ID, active.ID)
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

// --- GetUpcomingGamesByChatIDs ---

func TestGameRepo_GetUpcomingGamesByChatIDs_FiltersChats(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	future := time.Now().Add(48 * time.Hour)
	g1, _ := repo.Create(ctx, newGame(-1001, future, "1"))
	g2, _ := repo.Create(ctx, newGame(-1002, future, "2"))
	_, _ = repo.Create(ctx, newGame(-1003, future, "3"))

	games, err := repo.GetUpcomingGamesByChatIDs(ctx, []int64{-1001, -1002})
	if err != nil {
		t.Fatalf("GetUpcomingGamesByChatIDs: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("got %d games, want 2", len(games))
	}
	ids := map[int64]bool{games[0].ID: true, games[1].ID: true}
	if !ids[g1.ID] || !ids[g2.ID] {
		t.Errorf("unexpected game IDs in result: %v", games)
	}
}

func TestGameRepo_GetUpcomingGamesByChatIDs_ExcludesCompletedAndPast(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	future := time.Now().Add(48 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	active, _ := repo.Create(ctx, newGame(-1001, future, "1"))
	completed, _ := repo.Create(ctx, newGame(-1001, future, "2"))
	_ = repo.MarkCompleted(ctx, completed.ID)
	_, _ = repo.Create(ctx, newGame(-1001, past, "3"))

	games, err := repo.GetUpcomingGamesByChatIDs(ctx, []int64{-1001})
	if err != nil {
		t.Fatalf("GetUpcomingGamesByChatIDs: %v", err)
	}
	if len(games) != 1 || games[0].ID != active.ID {
		t.Errorf("got %d games (want 1 active), IDs: %v", len(games), games)
	}
}

func TestGameRepo_GetUpcomingGamesByChatIDs_EmptySlice(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	_, _ = repo.Create(ctx, newGame(-1001, time.Now().Add(48*time.Hour), "1"))

	games, err := repo.GetUpcomingGamesByChatIDs(ctx, []int64{})
	if err != nil {
		t.Fatalf("GetUpcomingGamesByChatIDs with empty list: %v", err)
	}
	if len(games) != 0 {
		t.Errorf("got %d games for empty chatIDs, want 0", len(games))
	}
}

// --- GetNextGameForTelegramUser ---

func TestGameRepo_GetNextGameForTelegramUser_Registered(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 900001})
	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(48*time.Hour), "1,2"))
	_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusRegistered)

	got, err := gameRepo.GetNextGameForTelegramUser(ctx, 900001)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got == nil {
		t.Fatal("expected a game, got nil")
	}
	if got.ID != g.ID {
		t.Errorf("got game ID %d, want %d", got.ID, g.ID)
	}
}

func TestGameRepo_GetNextGameForTelegramUser_NoGame(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	got, err := repo.GetNextGameForTelegramUser(ctx, 999999)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil game for unknown user, got game ID %d", got.ID)
	}
}

func TestGameRepo_GetNextGameForTelegramUser_SkippedNotReturned(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 900002})
	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(48*time.Hour), "1"))
	_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusSkipped)

	got, err := gameRepo.GetNextGameForTelegramUser(ctx, 900002)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got != nil {
		t.Errorf("skipped participation should not be returned, got game ID %d", got.ID)
	}
}

func TestGameRepo_GetNextGameForTelegramUser_CompletedNotReturned(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 900003})
	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(48*time.Hour), "1"))
	_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusRegistered)
	_ = gameRepo.MarkCompleted(ctx, g.ID)

	got, err := gameRepo.GetNextGameForTelegramUser(ctx, 900003)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got != nil {
		t.Errorf("completed game should not be returned, got game ID %d", got.ID)
	}
}

func TestGameRepo_GetNextGameForTelegramUser_ReturnsNearest(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 900004})
	near, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1"))
	far, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(96*time.Hour), "2"))
	_ = partRepo.Upsert(ctx, near.ID, p.ID, models.StatusRegistered)
	_ = partRepo.Upsert(ctx, far.ID, p.ID, models.StatusRegistered)

	got, err := gameRepo.GetNextGameForTelegramUser(ctx, 900004)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got == nil {
		t.Fatal("expected nearest game, got nil")
	}
	if got.ID != near.ID {
		t.Errorf("got game ID %d (far), want %d (near)", got.ID, near.ID)
	}
}

// --- GetGamesForPlayer ---

func TestGameRepo_GetGamesForPlayer_WithoutGroupRow(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 910001})
	g, _ := gameRepo.Create(ctx, newGame(-991001, time.Now().Add(24*time.Hour), "1,2"))
	_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusRegistered)

	games, err := gameRepo.GetGamesForPlayer(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetGamesForPlayer: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1", len(games))
	}
	if games[0].ID != g.ID {
		t.Errorf("game ID: got %d, want %d", games[0].ID, g.ID)
	}
	if games[0].GroupTitle != "Unknown group" {
		t.Errorf("group title fallback: got %q, want %q", games[0].GroupTitle, "Unknown group")
	}
	if games[0].Timezone != "UTC" {
		t.Errorf("timezone fallback: got %q, want %q", games[0].Timezone, "UTC")
	}
	if games[0].ParticipationStatus != string(models.StatusRegistered) {
		t.Errorf("participation status: got %q, want %q", games[0].ParticipationStatus, models.StatusRegistered)
	}
}

func TestGameRepo_GetGamesForPlayer_UsesGroupMetadata(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)
	groupRepo := storage.NewGroupRepo(testPool)

	const (
		chatID   = int64(-991002)
		title    = "Evening Squash"
		timezone = "Europe/Berlin"
	)

	if err := groupRepo.Upsert(ctx, chatID, title, true); err != nil {
		t.Fatalf("Upsert group: %v", err)
	}
	if err := groupRepo.SetTimezone(ctx, chatID, timezone); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}

	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 910002})
	g, _ := gameRepo.Create(ctx, newGame(chatID, time.Now().Add(24*time.Hour), "3,4"))
	_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusSkipped)

	games, err := gameRepo.GetGamesForPlayer(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetGamesForPlayer: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1", len(games))
	}
	if games[0].GroupTitle != title {
		t.Errorf("group title: got %q, want %q", games[0].GroupTitle, title)
	}
	if games[0].Timezone != timezone {
		t.Errorf("timezone: got %q, want %q", games[0].Timezone, timezone)
	}
	if games[0].ParticipationStatus != string(models.StatusSkipped) {
		t.Errorf("participation status: got %q, want %q", games[0].ParticipationStatus, models.StatusSkipped)
	}
}

// --- UpdateCourts ---

func TestGameRepo_UpdateCourts(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGameRepo(testPool)

	g, _ := repo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))

	if err := repo.UpdateCourts(ctx, g.ID, "3,4,5", 3); err != nil {
		t.Fatalf("UpdateCourts: %v", err)
	}

	updated, _ := repo.GetByID(ctx, g.ID)
	if updated.Courts != "3,4,5" {
		t.Errorf("Courts: got %q, want %q", updated.Courts, "3,4,5")
	}
	if updated.CourtsCount != 3 {
		t.Errorf("CourtsCount: got %d, want 3", updated.CourtsCount)
	}
}

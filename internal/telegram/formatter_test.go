package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/telegram"
)

func strPtr(s string) *string { return &s }

func makeGame(courts string, gameDate time.Time) *models.Game {
	return &models.Game{
		ID:          1,
		ChatID:      -1001,
		Courts:      courts,
		CourtsCount: strings.Count(courts, ",") + 1,
		GameDate:    gameDate,
	}
}

func makeParticipation(telegramID int64, username, firstName, lastName string, status models.ParticipationStatus) *models.GameParticipation {
	p := &models.Player{TelegramID: telegramID}
	if username != "" {
		p.Username = strPtr(username)
	}
	if firstName != "" {
		p.FirstName = strPtr(firstName)
	}
	if lastName != "" {
		p.LastName = strPtr(lastName)
	}
	return &models.GameParticipation{
		GameID:  1,
		PlayerID: telegramID,
		Player:  p,
		Status:  status,
	}
}

func TestFormatGameMessage_NoPlayers(t *testing.T) {
	game := makeGame("3,4", time.Date(2026, 3, 22, 18, 0, 0, 0, time.UTC))
	msg := telegram.FormatGameMessage(game, nil, time.UTC)

	if !strings.Contains(msg, "🏸 Squash Game") {
		t.Error("missing header")
	}
	if !strings.Contains(msg, "Courts: 3,4 (capacity: 4 players)") {
		t.Errorf("missing courts info, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Players (0/4):") {
		t.Errorf("missing players count, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Last updated:") {
		t.Error("missing last-updated footer")
	}
	if !strings.Contains(msg, "18:00") {
		t.Errorf("missing time, got:\n%s", msg)
	}
}

func TestFormatGameMessage_WithRegisteredPlayers(t *testing.T) {
	game := makeGame("1,2", time.Date(2026, 4, 15, 20, 0, 0, 0, time.UTC))
	parts := []*models.GameParticipation{
		makeParticipation(1, "alice", "", "", models.StatusRegistered),
		makeParticipation(2, "", "John", "Doe", models.StatusRegistered),
	}

	msg := telegram.FormatGameMessage(game, parts, time.UTC)

	if !strings.Contains(msg, "Players (2/4):") {
		t.Errorf("player count wrong, got:\n%s", msg)
	}
	if !strings.Contains(msg, "1. @alice") {
		t.Errorf("first player missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "2. John Doe") {
		t.Errorf("second player missing, got:\n%s", msg)
	}
}

func TestFormatGameMessage_SkippedPlayersExcluded(t *testing.T) {
	game := makeGame("1,2,3", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC))
	parts := []*models.GameParticipation{
		makeParticipation(1, "alice", "", "", models.StatusRegistered),
		makeParticipation(2, "bob", "", "", models.StatusSkipped),
		makeParticipation(3, "carol", "", "", models.StatusRegistered),
	}

	msg := telegram.FormatGameMessage(game, parts, time.UTC)

	// Only registered players count
	if !strings.Contains(msg, "Players (2/6):") {
		t.Errorf("player count wrong (should exclude skipped), got:\n%s", msg)
	}
	if !strings.Contains(msg, "@alice") {
		t.Error("alice should appear")
	}
	if !strings.Contains(msg, "@carol") {
		t.Error("carol should appear")
	}
	if strings.Contains(msg, "@bob") {
		t.Error("bob (skipped) should NOT appear in player list")
	}
}

func TestFormatGameMessage_PlayerDisplayName_UsernamePreferred(t *testing.T) {
	game := makeGame("1", time.Now().Add(24*time.Hour))
	parts := []*models.GameParticipation{
		makeParticipation(1, "username_wins", "FirstName", "LastName", models.StatusRegistered),
	}

	msg := telegram.FormatGameMessage(game, parts, time.UTC)

	if !strings.Contains(msg, "@username_wins") {
		t.Errorf("username should be preferred over full name, got:\n%s", msg)
	}
}

func TestFormatGameMessage_PlayerDisplayName_FallbackToFullName(t *testing.T) {
	game := makeGame("1", time.Now().Add(24*time.Hour))
	parts := []*models.GameParticipation{
		makeParticipation(1, "", "Jane", "Smith", models.StatusRegistered),
	}

	msg := telegram.FormatGameMessage(game, parts, time.UTC)

	if !strings.Contains(msg, "Jane Smith") {
		t.Errorf("full name should be used when username absent, got:\n%s", msg)
	}
}

func TestFormatGameMessage_DateFormat(t *testing.T) {
	// Sunday, March 22
	game := makeGame("1", time.Date(2026, 3, 22, 18, 0, 0, 0, time.UTC))
	msg := telegram.FormatGameMessage(game, nil, time.UTC)

	if !strings.Contains(msg, "Sunday") {
		t.Errorf("day name missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "March") {
		t.Errorf("month name missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "22") {
		t.Errorf("day number missing, got:\n%s", msg)
	}
}

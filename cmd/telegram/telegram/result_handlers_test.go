package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

func int64PtrResult(v int64) *int64 { return &v }

func oppPlayer() *models.Player {
	name := "bob"
	return &models.Player{ID: 2, TelegramID: 200, Username: &name}
}

// Keep the examples in the score prompt/error consistent with the existing validator.
func TestValidateResultScore_ScoreOrder(t *testing.T) {
	const selfID, opponentID int64 = 1, 2
	tests := []struct {
		name    string
		score   string
		winner  *int64
		wantErr bool
	}{
		{"self wins", "3:1", int64PtrResult(selfID), false},
		{"opponent wins", "1:3", int64PtrResult(opponentID), false},
		{"self wins to zero", "3:0", int64PtrResult(selfID), false},
		{"opponent wins to zero", "0:3", int64PtrResult(opponentID), false},
		{"self win reversed", "0:3", int64PtrResult(selfID), true},
		{"opponent win reversed", "3:0", int64PtrResult(opponentID), true},
		{"zero draw", "0:0", nil, false},
		{"zero with self selected remains accepted", "0:0", int64PtrResult(selfID), false},
		{"zero with opponent selected remains accepted", "0:0", int64PtrResult(opponentID), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wiz := &resultWizard{opponent: oppPlayer(), winnerID: tt.winner}
			err := validateResultScore(tt.score, wiz, int64PtrResult(selfID))
			if (err != nil) != tt.wantErr {
				t.Errorf("validateResultScore(%q): got %v, wantErr %v", tt.score, err, tt.wantErr)
			}
		})
	}
}

func buildApprovalCardBot() *Bot {
	return &Bot{}
}

func TestBuildApprovalCardText_Draw(t *testing.T) {
	b := buildApprovalCardBot()
	lz := i18n.New(i18n.En)
	result := &client.GameResultDTO{Score: "2:2"}
	wiz := &resultWizard{opponent: oppPlayer(), gameLabel: "Mon 02 Jan"}

	text := b.buildApprovalCardText(context.Background(), result, wiz, "@alice", lz)

	if !strings.Contains(text, "Draw") {
		t.Errorf("draw card should contain 'Draw', got: %q", text)
	}
	if strings.Contains(text, "@bob") {
		t.Errorf("draw card should not mention opponent name as winner, got: %q", text)
	}
}

func TestBuildApprovalCardText_OpponentWon(t *testing.T) {
	b := buildApprovalCardBot()
	lz := i18n.New(i18n.En)
	oppID := int64(2)
	result := &client.GameResultDTO{Score: "1:3", WinnerID: &oppID}
	wiz := &resultWizard{opponent: oppPlayer(), gameLabel: "Mon 02 Jan"}

	text := b.buildApprovalCardText(context.Background(), result, wiz, "@alice", lz)

	// From the opponent's perspective they won → "Me" in the Outcome line
	if !strings.Contains(text, "Me") {
		t.Errorf("opponent-won card should contain 'Me' (opponent's perspective), got: %q", text)
	}
	// The winner label must not name @alice (she lost); she may appear in the submission line.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Outcome:") && strings.Contains(line, "@alice") {
			t.Errorf("Outcome line must not show author '@alice' as winner, got: %q", line)
		}
	}
}

func TestBuildApprovalCardText_AuthorWon(t *testing.T) {
	b := buildApprovalCardBot()
	lz := i18n.New(i18n.En)
	authorID := int64(1)
	result := &client.GameResultDTO{Score: "3:1", WinnerID: &authorID}
	wiz := &resultWizard{opponent: oppPlayer(), gameLabel: "Mon 02 Jan"}

	text := b.buildApprovalCardText(context.Background(), result, wiz, "@alice", lz)

	// From the opponent's perspective the author won → show author name
	if !strings.Contains(text, "@alice") {
		t.Errorf("author-won card should show author display '@alice' as winner, got: %q", text)
	}
	if strings.Contains(text, "@bob") {
		t.Errorf("author-won card must not show opponent '@bob' as winner, got: %q", text)
	}
}

func TestBuildApprovalCardText_AuthorDisplayInRequestLine(t *testing.T) {
	b := buildApprovalCardBot()
	lz := i18n.New(i18n.En)
	result := &client.GameResultDTO{Score: ""}
	wiz := &resultWizard{opponent: oppPlayer(), gameLabel: "Mon 02 Jan"}

	text := b.buildApprovalCardText(context.Background(), result, wiz, "@charlie", lz)

	if !strings.Contains(text, "@charlie") {
		t.Errorf("approval request line must contain author display '@charlie', got: %q", text)
	}
}

func TestBuildApprovalCardText_EscapesAuthorMarkdown(t *testing.T) {
	b := buildApprovalCardBot()
	lz := i18n.New(i18n.En)
	authorID := int64(1)
	result := &client.GameResultDTO{Score: "3:1", WinnerID: &authorID}
	wiz := &resultWizard{opponent: oppPlayer(), gameLabel: "Mon 02 Jan"}

	text := b.buildApprovalCardText(context.Background(), result, wiz, "@john_doe", lz)

	if !strings.Contains(text, "@john\\_doe") {
		t.Errorf("author name with underscore must be escaped to '@john\\_doe', got: %q", text)
	}
	if strings.Contains(text, "@john_doe") {
		t.Errorf("unescaped author name '@john_doe' must not appear, got: %q", text)
	}
}

package telegram

import (
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// sendText/editText render with ParseMode=Markdown, so renderLeaderboard must
// escape any user-supplied string it interpolates. A raw _ or * in a name
// would otherwise unbalance Markdown and cause Telegram to reject the message.
func TestRenderLeaderboard_EscapesMarkdownInTitleAndNames(t *testing.T) {
	lz := i18n.New(i18n.En)

	firstName := "Bob_Risky"
	lastName := "*Star*"
	entries := []client.LeaderboardEntry{
		{
			Rank:        1,
			Player:      &models.Player{ID: 1, FirstName: &firstName, LastName: &lastName},
			Rating:      1600,
			GamesPlayed: 5,
		},
	}

	out := renderLeaderboard(entries, "Squash_Crew*Beta", lz)

	for _, raw := range []string{"Bob_Risky", "*Star*", "Squash_Crew*Beta"} {
		if strings.Contains(out, raw) {
			t.Errorf("renderLeaderboard left %q unescaped in output:\n%s", raw, out)
		}
	}
	for _, escaped := range []string{`Bob\_Risky`, `\*Star\*`, `Squash\_Crew\*Beta`} {
		if !strings.Contains(out, escaped) {
			t.Errorf("renderLeaderboard missing escaped form %q in output:\n%s", escaped, out)
		}
	}
}

func TestRenderLeaderboard_EmptyEntries(t *testing.T) {
	lz := i18n.New(i18n.En)

	got := renderLeaderboard(nil, "anything", lz)
	want := lz.T(i18n.MsgLeaderboardEmpty)
	if got != want {
		t.Errorf("empty render: got %q, want %q", got, want)
	}
}

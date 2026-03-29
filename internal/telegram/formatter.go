package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

// FormatGameMessage produces the announcement text for a squash game.
// loc is used to display the game date/time in the correct local timezone.
// now is used for the "Last updated" footer; callers pass time.Now().
// lz provides localised strings for the message content.
// guests are shown after the registered player list and count toward the total.
func FormatGameMessage(game *models.Game, participants []*models.GameParticipation, guests []*models.GuestParticipation, loc *time.Location, now time.Time, lz *i18n.Localizer) string {
	capacity := game.CourtsCount * 2

	var registered []*models.GameParticipation
	for _, p := range participants {
		if p.Status == models.StatusRegistered {
			registered = append(registered, p)
		}
	}

	totalCount := len(registered) + len(guests)
	localDate := game.GameDate.In(loc)

	var sb strings.Builder
	sb.WriteString(lz.T(i18n.GameHeader) + "\n\n")
	sb.WriteString(fmt.Sprintf("📅 %s · %s\n", lz.FormatGameDate(localDate), localDate.Format("15:04")))
	sb.WriteString(lz.Tf(i18n.GameCourts, game.Courts, capacity) + "\n\n")
	sb.WriteString(lz.Tf(i18n.GamePlayers, totalCount, capacity) + "\n")

	num := 1
	for _, p := range registered {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, playerDisplayName(p.Player)))
		num++
	}
	for _, g := range guests {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, lz.Tf(i18n.GameGuestLine, playerDisplayName(g.InvitedBy))))
		num++
	}

	sb.WriteString("\n" + lz.Tf(i18n.GameLastUpdated, lz.FormatUpdatedAt(now.In(loc))))
	return sb.String()
}

func playerDisplayName(p *models.Player) string {
	if p.Username != nil && *p.Username != "" {
		return "@" + *p.Username
	}
	var parts []string
	if p.FirstName != nil && *p.FirstName != "" {
		parts = append(parts, *p.FirstName)
	}
	if p.LastName != nil && *p.LastName != "" {
		parts = append(parts, *p.LastName)
	}
	return strings.Join(parts, " ")
}

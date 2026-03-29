package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
)

// FormatGameMessage produces the announcement text for a squash game.
// loc is used to display the game date/time in the correct local timezone.
// now is used for the "Last updated" footer; callers pass time.Now().
// guests are shown after the registered player list and count toward the total.
func FormatGameMessage(game *models.Game, participants []*models.GameParticipation, guests []*models.GuestParticipation, loc *time.Location, now time.Time) string {
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
	sb.WriteString("🏸 Squash Game\n\n")
	sb.WriteString(fmt.Sprintf("📅 %s · %s\n", formatGameDate(localDate), localDate.Format("15:04")))
	sb.WriteString(fmt.Sprintf("🎾 Courts: %s (capacity: %d players)\n\n", game.Courts, capacity))
	sb.WriteString(fmt.Sprintf("Players (%d/%d):\n", totalCount, capacity))

	num := 1
	for _, p := range registered {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, playerDisplayName(p.Player)))
		num++
	}
	for _, g := range guests {
		sb.WriteString(fmt.Sprintf("%d. +1 (by %s)\n", num, playerDisplayName(g.InvitedBy)))
		num++
	}

	sb.WriteString(fmt.Sprintf("\nLast updated: %s", formatUpdatedAt(now.In(loc))))
	return sb.String()
}

func formatGameDate(t time.Time) string {
	return fmt.Sprintf("%s, %s %d", t.Weekday(), t.Format("January"), t.Day())
}

func formatUpdatedAt(t time.Time) string {
	return fmt.Sprintf("%d %s %d, %s", t.Day(), t.Format("Jan"), t.Year(), t.Format("15:04"))
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

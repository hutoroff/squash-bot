package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// PostLeaderboardJob posts a daily group leaderboard 24 h after the day's last game start.
// Runs on every 5-min poll; per-group dedup via bot_groups.last_leaderboard_posted_for.
type PostLeaderboardJob struct {
	api        TelegramAPI
	groupRepo  GroupRepository
	gameRepo   GameRepository
	resultRepo GameResultRepository
	ratingSvc  *RatingService
	loc        *time.Location
	logger     *slog.Logger
}

func NewPostLeaderboardJob(
	api TelegramAPI,
	groupRepo GroupRepository,
	gameRepo GameRepository,
	resultRepo GameResultRepository,
	ratingSvc *RatingService,
	loc *time.Location,
	logger *slog.Logger,
) *PostLeaderboardJob {
	return &PostLeaderboardJob{
		api:        api,
		groupRepo:  groupRepo,
		gameRepo:   gameRepo,
		resultRepo: resultRepo,
		ratingSvc:  ratingSvc,
		loc:        loc,
		logger:     logger,
	}
}

func (j *PostLeaderboardJob) name() string   { return "post_leaderboard" }
func (j *PostLeaderboardJob) run(force bool) { j.runPostLeaderboard(force) }

func (j *PostLeaderboardJob) runPostLeaderboard(force bool) {
	ctx := context.Background()
	groups, err := j.groupRepo.GetAll(ctx)
	if err != nil {
		j.logger.Error("post_leaderboard: get groups", "err", err)
		return
	}

	for _, g := range groups {
		j.processGroup(ctx, &g, force)
	}
}

func (j *PostLeaderboardJob) processGroup(ctx context.Context, g *models.Group, force bool) {
	loc, err := time.LoadLocation(g.Timezone)
	if err != nil {
		loc = j.loc
	}

	// Find candidate day D: most recent game_date with completed games that is ≥ 24 h ago.
	now := time.Now().In(loc)
	cutoff := now.Add(-24 * time.Hour)
	candidateDate := cutoff.Truncate(24 * time.Hour)

	// Check if we already posted for this day.
	if !force && g.LastLeaderboardPostedFor != nil {
		posted := g.LastLeaderboardPostedFor.In(loc)
		if !posted.Before(candidateDate) {
			return
		}
	}

	// Fetch approved results for the candidate day.
	results, err := j.resultRepo.ListByGroupAndDate(ctx, g.ChatID, candidateDate)
	if err != nil {
		j.logger.Error("post_leaderboard: list results", "group_id", g.ChatID, "err", err)
		return
	}

	// Filter to approved/auto_approved.
	var approved []*models.GameResult
	for _, r := range results {
		if r.Status == models.GameResultApproved || r.Status == models.GameResultAutoApproved {
			approved = append(approved, r)
		}
	}

	// Mark posted even if no results to avoid re-checking repeatedly.
	if err := j.groupRepo.SetLastLeaderboardPostedFor(ctx, g.ChatID, candidateDate); err != nil {
		j.logger.Warn("post_leaderboard: set last_leaderboard_posted_for", "group_id", g.ChatID, "err", err)
	}

	if len(approved) == 0 {
		return
	}

	// Build and post the leaderboard.
	entries, err := j.ratingSvc.GetLeaderboard(ctx, g.ChatID)
	if err != nil {
		j.logger.Error("post_leaderboard: get leaderboard", "group_id", g.ChatID, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	text := formatLeaderboard(entries, candidateDate, loc)
	msg := tgbotapi.NewMessage(g.ChatID, text)
	msg.ParseMode = "Markdown"
	msg.DisableNotification = true
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Warn("post_leaderboard: send message", "group_id", g.ChatID, "err", err)
	}
}

func formatLeaderboard(entries []LeaderboardEntry, date time.Time, loc *time.Location) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🏆 Leaderboard — %s\n\n", date.In(loc).Format("02 Jan 2006")))
	for _, e := range entries {
		name := ""
		if e.Player != nil {
			name = playerDisplayForLB(e.Player)
		}
		delta := ""
		if e.DeltaToday > 0.5 {
			delta = fmt.Sprintf("   ▲+%.0f", e.DeltaToday)
		} else if e.DeltaToday < -0.5 {
			delta = fmt.Sprintf("   ▼%.0f", e.DeltaToday)
		}
		sb.WriteString(fmt.Sprintf("%d.  %-16s %4.0f (%dg)%s\n",
			e.Rank, name, e.Rating, e.GamesPlayed, delta))
	}
	return sb.String()
}

func playerDisplayForLB(p *models.Player) string {
	if p.FirstName != nil && *p.FirstName != "" {
		name := *p.FirstName
		if p.LastName != nil && *p.LastName != "" {
			name += " " + *p.LastName
		}
		return name
	}
	if p.Username != nil && *p.Username != "" {
		return "@" + *p.Username
	}
	return fmt.Sprintf("player#%d", p.ID)
}

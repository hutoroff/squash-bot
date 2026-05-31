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

	// Candidate day is yesterday in the group's local timezone. Compute it from
	// year/month/day so the day-boundary respects loc, not UTC.
	nowLocal := time.Now().In(loc)
	yesterday := nowLocal.AddDate(0, 0, -1)
	candidateDate := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, loc)
	candidateEnd := candidateDate.Add(24 * time.Hour)

	if !force && g.LastLeaderboardPostedFor != nil {
		posted := g.LastLeaderboardPostedFor.In(loc)
		if !posted.Before(candidateDate) {
			return
		}
	}

	// Gate on "24 h after the day's last game start". If the last game on the
	// candidate day started less than 24 h ago, retry on the next poll instead
	// of marking the day done early.
	completedGames, err := j.gameRepo.GetCompletedGamesByGroupAndDay(ctx, g.ChatID, candidateDate, candidateEnd)
	if err != nil {
		j.logger.Error("post_leaderboard: list completed games", "group_id", g.ChatID, "err", err)
		return
	}
	if !force && len(completedGames) > 0 {
		var lastStart time.Time
		for _, cg := range completedGames {
			if cg.GameDate.After(lastStart) {
				lastStart = cg.GameDate
			}
		}
		if time.Now().Before(lastStart.Add(24 * time.Hour)) {
			return
		}
	}

	results, err := j.resultRepo.ListByGroupAndDate(ctx, g.ChatID, candidateDate)
	if err != nil {
		j.logger.Error("post_leaderboard: list results", "group_id", g.ChatID, "err", err)
		return
	}

	var approved []*models.GameResult
	for _, r := range results {
		if r.Status == models.GameResultApproved || r.Status == models.GameResultAutoApproved {
			approved = append(approved, r)
		}
	}

	if len(approved) == 0 {
		// Terminal no-op: mark the day done so we don't re-check forever.
		if err := j.groupRepo.SetLastLeaderboardPostedFor(ctx, g.ChatID, candidateDate); err != nil {
			j.logger.Warn("post_leaderboard: set last_leaderboard_posted_for", "group_id", g.ChatID, "err", err)
		}
		return
	}

	entries, err := j.ratingSvc.GetLeaderboard(ctx, g.ChatID)
	if err != nil {
		j.logger.Error("post_leaderboard: get leaderboard", "group_id", g.ChatID, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	// Plain text — names can contain Markdown control characters and the
	// message has no markdown formatting that needs interpreting.
	text := formatLeaderboard(entries, candidateDate, loc)
	msg := tgbotapi.NewMessage(g.ChatID, text)
	msg.DisableNotification = true
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Warn("post_leaderboard: send message", "group_id", g.ChatID, "err", err)
		return
	}

	// Only mark the day done after a successful send so transient failures
	// (e.g. Telegram rate-limit) retry on the next poll.
	if err := j.groupRepo.SetLastLeaderboardPostedFor(ctx, g.ChatID, candidateDate); err != nil {
		j.logger.Warn("post_leaderboard: set last_leaderboard_posted_for", "group_id", g.ChatID, "err", err)
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

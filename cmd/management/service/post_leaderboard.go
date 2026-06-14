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

// leaderboardLookbackDays is how far back the job searches for un-posted game
// days. 5 days covers the 48 h auto-approve window + 24 h gate + downtime margin.
const leaderboardLookbackDays = 5

// leaderboardPendingGraceWindow bounds how long a pending result keeps its day
// open. Auto-approve resolves pending rows at 48 h, so anything older should
// already be decided; the cap prevents a wedged row from blocking all future posts.
const leaderboardPendingGraceWindow = 49 * time.Hour

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
	if !g.LeaderboardNotificationsEnabled {
		return
	}

	loc, err := time.LoadLocation(g.Timezone)
	if err != nil {
		loc = j.loc
	}

	// Use time.Date with loc so day boundaries respect the group's timezone,
	// never Truncate(24h) which truncates in UTC.
	now := time.Now().In(loc)
	today0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterday0 := today0.AddDate(0, 0, -1)
	lookbackStart := today0.AddDate(0, 0, -leaderboardLookbackDays)

	// Start from the day after the marker, clamped to the look-back window.
	// force bypasses the 24h gate (handled per-day) but still honors the marker
	// so a manual trigger never re-posts days already posted.
	var start time.Time
	if g.LastLeaderboardPostedFor != nil {
		markerDay := g.LastLeaderboardPostedFor.In(loc)
		markerDay = time.Date(markerDay.Year(), markerDay.Month(), markerDay.Day(), 0, 0, 0, 0, loc)
		next := markerDay.AddDate(0, 0, 1)
		if next.Before(lookbackStart) {
			start = lookbackStart
		} else {
			start = next
		}
	} else {
		start = lookbackStart
	}

	// Iterate oldest-first so messages are chronological and the marker advances
	// monotonically. Stop at the first day that cannot be resolved yet.
	for day := start; !day.After(yesterday0); day = day.AddDate(0, 0, 1) {
		if !j.processCandidateDay(ctx, g, loc, day, force) {
			return
		}
	}
}

// processCandidateDay processes one candidate day. Returns true when the loop
// should advance past this day (posted or silently skipped), false to stop and
// retry on the next poll (waiting on gate / pending / transient error).
func (j *PostLeaderboardJob) processCandidateDay(ctx context.Context, g *models.Group, loc *time.Location, candidateDate time.Time, force bool) bool {
	candidateEnd := candidateDate.Add(24 * time.Hour)

	results, err := j.resultRepo.ListByGroupAndDate(ctx, g.ChatID, candidateDate)
	if err != nil {
		j.logger.Error("post_leaderboard: list results", "group_id", g.ChatID, "date", candidateDate.Format("2006-01-02"), "err", err)
		return false
	}

	var approved []*models.GameResult
	pendingBlocking := 0
	now := time.Now()
	for _, r := range results {
		switch r.Status {
		case models.GameResultApproved, models.GameResultAutoApproved:
			approved = append(approved, r)
		case models.GameResultPending:
			if now.Before(r.SubmittedAt.Add(leaderboardPendingGraceWindow)) {
				pendingBlocking++
			}
		}
	}

	if len(approved) > 0 {
		// 24 h gate: wait until 24 h after the last game started on this day.
		completedGames, err := j.gameRepo.GetCompletedGamesByGroupAndDay(ctx, g.ChatID, candidateDate, candidateEnd)
		if err != nil {
			j.logger.Error("post_leaderboard: list completed games", "group_id", g.ChatID, "date", candidateDate.Format("2006-01-02"), "err", err)
			return false
		}
		if !force && len(completedGames) > 0 {
			var lastStart time.Time
			for _, cg := range completedGames {
				if cg.GameDate.After(lastStart) {
					lastStart = cg.GameDate
				}
			}
			if now.Before(lastStart.Add(24 * time.Hour)) {
				return false
			}
		}

		entries, err := j.ratingSvc.GetLeaderboard(ctx, g.ChatID)
		if err != nil {
			j.logger.Error("post_leaderboard: get leaderboard", "group_id", g.ChatID, "err", err)
			return false
		}
		if len(entries) == 0 {
			return false
		}

		// GetLeaderboard returns CURRENT standings, and its DeltaToday is computed
		// against the run day's local window — not candidateDate. That delta is
		// only meaningful for "yesterday" (the common late-approval case where the
		// approval applied today). For older catch-up days the delta belongs to
		// today, not that day, so suppress it to avoid a misleading ▲/▼.
		nowLoc := time.Now().In(loc)
		yesterday0 := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
		showDelta := candidateDate.Equal(yesterday0)

		// Plain text — names can contain Markdown control characters and the
		// message has no markdown formatting that needs interpreting.
		text := formatLeaderboard(entries, candidateDate, loc, showDelta)
		msg := tgbotapi.NewMessage(g.ChatID, text)
		msg.DisableNotification = true
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Warn("post_leaderboard: send message", "group_id", g.ChatID, "err", err)
			return false
		}

		// Only mark the day done after a successful send so transient failures
		// (e.g. Telegram rate-limit) retry on the next poll.
		if err := j.groupRepo.SetLastLeaderboardPostedFor(ctx, g.ChatID, candidateDate); err != nil {
			j.logger.Warn("post_leaderboard: set last_leaderboard_posted_for", "group_id", g.ChatID, "err", err)
		}
		return true
	}

	// Zero approved results.
	if !force && pendingBlocking > 0 {
		// Fresh pending results exist; keep the day open — auto-approve will
		// resolve them within 48 h and the next poll will post.
		return false
	}

	// No approved results and no fresh pending (rejected/canceled only, or
	// pending rows past the grace window). Silently advance without posting.
	if err := j.groupRepo.SetLastLeaderboardPostedFor(ctx, g.ChatID, candidateDate); err != nil {
		j.logger.Warn("post_leaderboard: set last_leaderboard_posted_for", "group_id", g.ChatID, "err", err)
	}
	return true
}

func formatLeaderboard(entries []LeaderboardEntry, date time.Time, loc *time.Location, showDelta bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🏆 Leaderboard — %s\n\n", date.In(loc).Format("02 Jan 2006")))
	for _, e := range entries {
		name := ""
		if e.Player != nil {
			name = playerDisplayForLB(e.Player)
		}
		delta := ""
		if showDelta && e.DeltaToday > 0.5 {
			delta = fmt.Sprintf("   ▲+%.0f", e.DeltaToday)
		} else if showDelta && e.DeltaToday < -0.5 {
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

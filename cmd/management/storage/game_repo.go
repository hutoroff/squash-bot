package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GameRepo struct {
	pool *pgxpool.Pool
}

func NewGameRepo(pool *pgxpool.Pool) *GameRepo {
	return &GameRepo{pool: pool}
}

func (r *GameRepo) Create(ctx context.Context, game *models.Game) (*models.Game, error) {
	const q = `
		INSERT INTO games (chat_id, game_date, courts_count, courts, venue_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		          notified_day_before, completed, created_at`

	slog.Debug("GameRepo.Create", "chat_id", game.ChatID, "game_date", game.GameDate)

	row := r.pool.QueryRow(ctx, q, game.ChatID, game.GameDate, game.CourtsCount, game.Courts, game.VenueID)
	return scanGame(row)
}

func (r *GameRepo) GetByID(ctx context.Context, id int64) (*models.Game, error) {
	const q = `
		SELECT g.id, g.chat_id, g.message_id, g.game_date, g.courts_count, g.courts, g.venue_id,
		       g.notified_day_before, g.completed, g.created_at,
		       v.id, v.group_id, v.name, v.courts, v.time_slots, v.address, v.created_at,
		       v.grace_period_hours, v.game_days, v.booking_opens_days, v.preventive_cancellation_fraction, v.last_booking_reminder_at,
		       v.preferred_game_times, v.last_auto_booking_at, v.auto_booking_courts
		FROM games g
		LEFT JOIN venues v ON v.id = g.venue_id
		WHERE g.id = $1`

	slog.Debug("GameRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanGameWithVenue(row)
}

func (r *GameRepo) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE completed = false AND game_date > now()
		ORDER BY game_date`

	slog.Debug("GameRepo.GetUpcomingGames")

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query upcoming games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

func (r *GameRepo) UpdateMessageID(ctx context.Context, gameID, messageID int64) error {
	const q = `UPDATE games SET message_id = $1 WHERE id = $2`
	slog.Debug("GameRepo.UpdateMessageID", "game_id", gameID, "message_id", messageID)
	_, err := r.pool.Exec(ctx, q, messageID, gameID)
	return err
}

// GetGamesForDayBefore returns games scheduled in [from, to) where notified_day_before is false.
func (r *GameRepo) GetGamesForDayBefore(ctx context.Context, from, to time.Time) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE game_date >= $1 AND game_date < $2
		  AND notified_day_before = false
		  AND completed = false`

	slog.Debug("GameRepo.GetGamesForDayBefore", "from", from, "to", to)

	rows, err := r.pool.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("query day-before games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// GetGamesForDayAfter returns games scheduled in [from, to) where completed is false and message_id is set.
func (r *GameRepo) GetGamesForDayAfter(ctx context.Context, from, to time.Time) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE game_date >= $1 AND game_date < $2
		  AND completed = false
		  AND message_id IS NOT NULL`

	slog.Debug("GameRepo.GetGamesForDayAfter", "from", from, "to", to)

	rows, err := r.pool.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("query day-after games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

func (r *GameRepo) MarkNotifiedDayBefore(ctx context.Context, gameID int64) error {
	const q = `UPDATE games SET notified_day_before = true WHERE id = $1`
	slog.Debug("GameRepo.MarkNotifiedDayBefore", "game_id", gameID)
	_, err := r.pool.Exec(ctx, q, gameID)
	return err
}

// GetUpcomingGamesForFinalCheck returns all future uncompleted games where final_court_check_done
// is false, with venue data joined. Used by FinalCourtCheckJob.
func (r *GameRepo) GetUpcomingGamesForFinalCheck(ctx context.Context) ([]*models.Game, error) {
	const q = `
		SELECT g.id, g.chat_id, g.message_id, g.game_date, g.courts_count, g.courts, g.venue_id,
		       g.notified_day_before, g.completed, g.created_at,
		       v.id, v.group_id, v.name, v.courts, v.time_slots, v.address, v.created_at,
		       v.grace_period_hours, v.game_days, v.booking_opens_days, v.preventive_cancellation_fraction, v.last_booking_reminder_at,
		       v.preferred_game_times, v.last_auto_booking_at, v.auto_booking_courts
		FROM games g
		LEFT JOIN venues v ON v.id = g.venue_id
		WHERE g.completed = false
		  AND g.final_court_check_done = false
		  AND g.game_date > now()
		ORDER BY g.game_date`

	slog.Debug("GameRepo.GetUpcomingGamesForFinalCheck")

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query upcoming games for final check: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGameWithVenue(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

func (r *GameRepo) MarkFinalCourtCheckDone(ctx context.Context, gameID int64) error {
	const q = `UPDATE games SET final_court_check_done = true WHERE id = $1`
	slog.Debug("GameRepo.MarkFinalCourtCheckDone", "game_id", gameID)
	_, err := r.pool.Exec(ctx, q, gameID)
	return err
}

// GetUpcomingGamesForHalfwayCheck returns all future uncompleted games where halfway_court_check_done
// is false, with venue data joined. Used by HalfwayCourtCheckJob.
func (r *GameRepo) GetUpcomingGamesForHalfwayCheck(ctx context.Context) ([]*models.Game, error) {
	const q = `
		SELECT g.id, g.chat_id, g.message_id, g.game_date, g.courts_count, g.courts, g.venue_id,
		       g.notified_day_before, g.completed, g.created_at,
		       v.id, v.group_id, v.name, v.courts, v.time_slots, v.address, v.created_at,
		       v.grace_period_hours, v.game_days, v.booking_opens_days, v.preventive_cancellation_fraction, v.last_booking_reminder_at,
		       v.preferred_game_times, v.last_auto_booking_at, v.auto_booking_courts
		FROM games g
		LEFT JOIN venues v ON v.id = g.venue_id
		WHERE g.completed = false
		  AND g.halfway_court_check_done = false
		  AND g.game_date > now()
		ORDER BY g.game_date`

	slog.Debug("GameRepo.GetUpcomingGamesForHalfwayCheck")

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query upcoming games for halfway check: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGameWithVenue(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

func (r *GameRepo) MarkHalfwayCourtCheckDone(ctx context.Context, gameID int64) error {
	const q = `UPDATE games SET halfway_court_check_done = true WHERE id = $1`
	slog.Debug("GameRepo.MarkHalfwayCourtCheckDone", "game_id", gameID)
	_, err := r.pool.Exec(ctx, q, gameID)
	return err
}

func (r *GameRepo) MarkCompleted(ctx context.Context, gameID int64) error {
	const q = `UPDATE games SET completed = true WHERE id = $1`
	slog.Debug("GameRepo.MarkCompleted", "game_id", gameID)
	_, err := r.pool.Exec(ctx, q, gameID)
	return err
}

// GetUpcomingGamesByChatIDs returns upcoming games for the given chat IDs.
func (r *GameRepo) GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE completed = false AND game_date > now()
		  AND chat_id = ANY($1)
		ORDER BY game_date`

	slog.Debug("GameRepo.GetUpcomingGamesByChatIDs", "chat_ids", chatIDs)

	rows, err := r.pool.Query(ctx, q, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("query upcoming games by chat ids: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// GetNextGameForUser returns the nearest upcoming game where the user is registered.
// Returns nil, nil if the user has no upcoming registered games.
func (r *GameRepo) GetNextGameForUser(ctx context.Context, userID int64) (*models.Game, error) {
	const q = `
		SELECT g.id, g.chat_id, g.message_id, g.game_date, g.courts_count, g.courts, g.venue_id,
		       g.notified_day_before, g.completed, g.created_at,
		       v.id, v.group_id, v.name, v.courts, v.time_slots, v.address, v.created_at,
		       v.grace_period_hours, v.game_days, v.booking_opens_days, v.preventive_cancellation_fraction, v.last_booking_reminder_at,
		       v.preferred_game_times, v.last_auto_booking_at, v.auto_booking_courts
		FROM games g
		JOIN game_participations gp ON gp.game_id = g.id
		JOIN players p ON p.id = gp.player_id
		LEFT JOIN venues v ON v.id = g.venue_id
		WHERE p.user_id = $1 AND gp.status = 'registered'
		  AND g.completed = false AND g.game_date > now()
		ORDER BY g.game_date
		LIMIT 1`

	slog.Debug("GameRepo.GetNextGameForUser", "user_id", userID)

	row := r.pool.QueryRow(ctx, q, userID)
	g, err := scanGameWithVenue(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return g, nil
}

// GetUpcomingUnnotifiedGames returns all future uncompleted games where notified_day_before is false,
// with their venue data joined. Used by the cancellation reminder scheduler.
func (r *GameRepo) GetUpcomingUnnotifiedGames(ctx context.Context) ([]*models.Game, error) {
	const q = `
		SELECT g.id, g.chat_id, g.message_id, g.game_date, g.courts_count, g.courts, g.venue_id,
		       g.notified_day_before, g.completed, g.created_at,
		       v.id, v.group_id, v.name, v.courts, v.time_slots, v.address, v.created_at,
		       v.grace_period_hours, v.game_days, v.booking_opens_days, v.preventive_cancellation_fraction, v.last_booking_reminder_at,
		       v.preferred_game_times, v.last_auto_booking_at, v.auto_booking_courts
		FROM games g
		LEFT JOIN venues v ON v.id = g.venue_id
		WHERE g.completed = false
		  AND g.notified_day_before = false
		  AND g.game_date > now()
		ORDER BY g.game_date`

	slog.Debug("GameRepo.GetUpcomingUnnotifiedGames")

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query upcoming unnotified games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGameWithVenue(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// GetUncompletedGamesByGroupAndDay returns uncompleted games for a specific group within a day range.
// Used by the per-group day-after cleanup scheduler.
func (r *GameRepo) GetUncompletedGamesByGroupAndDay(ctx context.Context, chatID int64, from, to time.Time) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE chat_id = $1
		  AND game_date >= $2 AND game_date < $3
		  AND completed = false`

	slog.Debug("GameRepo.GetUncompletedGamesByGroupAndDay", "chat_id", chatID, "from", from, "to", to)

	rows, err := r.pool.Query(ctx, q, chatID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query group day games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// GetGamesForPlayer returns all games in which playerID has any participation record
// (registered or skipped), ordered newest-first. Each row is denormalised with the
// group timezone, venue details, and the current registered-player count so the
// caller does not need further queries.
func (r *GameRepo) GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	const q = `
		SELECT g.id, g.game_date, g.courts_count, g.courts, g.completed,
		       gp.status,
		       (SELECT COUNT(*) FROM game_participations gp2
		        WHERE gp2.game_id = g.id AND gp2.status = 'registered')
		       + (SELECT COUNT(*) FROM guest_participations gst
		          WHERE gst.game_id = g.id) AS participant_count,
                       COALESCE(v.name, '')    AS venue_name,
                       COALESCE(v.address, '') AS venue_address,
                       COALESCE(NULLIF(bg.title, ''), 'Unknown group') AS group_title,
                       COALESCE(NULLIF(bg.timezone, ''), 'UTC')        AS timezone
                FROM games g
                JOIN game_participations gp ON gp.game_id = g.id AND gp.player_id = $1
                LEFT JOIN venues v ON v.id = g.venue_id
                LEFT JOIN bot_groups bg ON bg.chat_id = g.chat_id
                ORDER BY g.game_date DESC`

	slog.Debug("GameRepo.GetGamesForPlayer", "player_id", playerID)

	rows, err := r.pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, fmt.Errorf("query player games: %w", err)
	}
	defer rows.Close()

	var games []models.PlayerGame
	for rows.Next() {
		var pg models.PlayerGame
		if err := rows.Scan(
			&pg.ID, &pg.GameDate, &pg.CourtsCount, &pg.Courts, &pg.Completed,
			&pg.ParticipationStatus, &pg.ParticipantCount,
			&pg.VenueName, &pg.VenueAddress,
			&pg.GroupTitle, &pg.Timezone,
		); err != nil {
			return nil, fmt.Errorf("scan player game: %w", err)
		}
		games = append(games, pg)
	}
	return games, rows.Err()
}

// ListGroupIDsForPlayer returns the distinct chat_ids of all groups in which
// the player has at least one participation record.
func (r *GameRepo) ListGroupIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	const q = `
		SELECT DISTINCT g.chat_id
		FROM game_participations gp
		JOIN games g ON g.id = gp.game_id
		WHERE gp.player_id = $1
		ORDER BY g.chat_id`

	rows, err := r.pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, fmt.Errorf("list group ids for player: %w", err)
	}
	defer rows.Close()

	var groupIDs []int64
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, fmt.Errorf("scan group_id: %w", err)
		}
		groupIDs = append(groupIDs, gid)
	}
	return groupIDs, rows.Err()
}

// PlayerCanAccessGame reports whether the user has any participation record
// (registered or skipped) in any game within the same chat as gameID's game.
// Returns false (not an error) when gameID does not exist.
func (r *GameRepo) PlayerCanAccessGame(ctx context.Context, userID, gameID int64) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM games target
			JOIN games g ON g.chat_id = target.chat_id
			JOIN game_participations gp ON gp.game_id = g.id
			JOIN players p ON p.id = gp.player_id
			WHERE target.id = $1 AND p.user_id = $2
		)`

	slog.Debug("GameRepo.PlayerCanAccessGame", "user_id", userID, "game_id", gameID)

	var allowed bool
	if err := r.pool.QueryRow(ctx, q, gameID, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("player can access game: %w", err)
	}
	return allowed, nil
}

// GetCompletedGamesByGroupAndDay returns completed games for a group whose
// game_date falls in [from, to). Used by PostLeaderboardJob to gate posting
// on "24 h after the day's last game start".
func (r *GameRepo) GetCompletedGamesByGroupAndDay(ctx context.Context, chatID int64, from, to time.Time) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts, venue_id,
		       notified_day_before, completed, created_at
		FROM games
		WHERE chat_id = $1
		  AND game_date >= $2 AND game_date < $3
		  AND completed = true`

	slog.Debug("GameRepo.GetCompletedGamesByGroupAndDay", "chat_id", chatID, "from", from, "to", to)

	rows, err := r.pool.Query(ctx, q, chatID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query completed group day games: %w", err)
	}
	defer rows.Close()

	var games []*models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// GetRecentCompletedGamesForPlayer returns past games for a user in a
// specific group that fall within the result-submission window. A game is
// eligible when game_date <= NOW() (start time has passed) AND its local calendar
// day (group timezone) is today or up to `days` days ago. The `completed` flag is
// intentionally NOT considered. Used by the /result wizard game picker. Mirrors the
// window predicate in GameInResultWindow.
func (r *GameRepo) GetRecentCompletedGamesForPlayer(ctx context.Context, userID, groupID int64, days int) ([]models.PlayerGame, error) {
	const q = `
		SELECT g.id, g.game_date, g.courts_count, g.courts, g.completed,
		       gp.status,
		       (SELECT COUNT(*) FROM game_participations gp2
		        WHERE gp2.game_id = g.id AND gp2.status = 'registered')
		       + (SELECT COUNT(*) FROM guest_participations gst
		          WHERE gst.game_id = g.id) AS participant_count,
		       COALESCE(v.name, '')    AS venue_name,
		       COALESCE(v.address, '') AS venue_address,
		       COALESCE(NULLIF(bg.title, ''), 'Unknown group') AS group_title,
		       COALESCE(NULLIF(bg.timezone, ''), 'UTC')        AS timezone
		FROM games g
		JOIN game_participations gp ON gp.game_id = g.id
		JOIN players p ON p.id = gp.player_id AND p.user_id = $1
		LEFT JOIN venues v ON v.id = g.venue_id
		LEFT JOIN bot_groups bg ON bg.chat_id = g.chat_id
		WHERE g.chat_id = $2
		  AND g.game_date <= NOW()
		  AND (NOW() AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
		      - (g.game_date AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
		      BETWEEN 0 AND $3
		ORDER BY g.game_date DESC`

	slog.Debug("GameRepo.GetRecentCompletedGamesForPlayer", "user_id", userID, "group_id", groupID, "days", days)

	rows, err := r.pool.Query(ctx, q, userID, groupID, days)
	if err != nil {
		return nil, fmt.Errorf("query recent completed games for player: %w", err)
	}
	defer rows.Close()

	var games []models.PlayerGame
	for rows.Next() {
		var pg models.PlayerGame
		if err := rows.Scan(
			&pg.ID, &pg.GameDate, &pg.CourtsCount, &pg.Courts, &pg.Completed,
			&pg.ParticipationStatus, &pg.ParticipantCount,
			&pg.VenueName, &pg.VenueAddress,
			&pg.GroupTitle, &pg.Timezone,
		); err != nil {
			return nil, fmt.Errorf("scan player game: %w", err)
		}
		games = append(games, pg)
	}
	return games, rows.Err()
}

// GameInResultWindow reports whether a result may still be submitted for the game:
// its local day (in the group's timezone) must be today or up to `days` days ago.
// Future games and games older than the window return false. Single source of truth
// for the result-submission window predicate, mirrored by GetRecentCompletedGamesForPlayer.
func (r *GameRepo) GameInResultWindow(ctx context.Context, gameID int64, days int) (bool, error) {
	const q = `
		SELECT g.game_date <= NOW()
		   AND (NOW() AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
		       - (g.game_date AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
		       BETWEEN 0 AND $2
		FROM games g
		LEFT JOIN bot_groups bg ON bg.chat_id = g.chat_id
		WHERE g.id = $1`

	var inWindow bool
	if err := r.pool.QueryRow(ctx, q, gameID, days).Scan(&inWindow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, pgx.ErrNoRows
		}
		return false, fmt.Errorf("query game result window: %w", err)
	}
	return inWindow, nil
}

// UpdateCourts updates the courts and courts_count for a game.
func (r *GameRepo) UpdateCourts(ctx context.Context, gameID int64, courts string, courtsCount int) error {
	const q = `UPDATE games SET courts = $1, courts_count = $2 WHERE id = $3`
	slog.Debug("GameRepo.UpdateCourts", "game_id", gameID, "courts", courts)
	_, err := r.pool.Exec(ctx, q, courts, courtsCount, gameID)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGame(s scanner) (*models.Game, error) {
	var g models.Game
	err := s.Scan(
		&g.ID, &g.ChatID, &g.MessageID, &g.GameDate, &g.CourtsCount, &g.Courts, &g.VenueID,
		&g.NotifiedDayBefore, &g.Completed, &g.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan game: %w", err)
	}
	return &g, nil
}

// scanGameWithVenue scans a game row that includes LEFT JOIN venue columns.
// All venue columns are nullable (NULL when no venue is linked).
func scanGameWithVenue(s scanner) (*models.Game, error) {
	var g models.Game
	var (
		venueID                             *int64
		venueGroupID                        *int64
		venueName                           *string
		venueCourts                         *string
		venueSlots                          *string
		venueAddr                           *string
		venueCreated                        *time.Time
		venueGracePeriodHours               *int
		venueGameDays                       *string
		venueBookingOpensDays               *int
		venuePreventiveCancellationFraction *string
		venueLastBookingReminder            *time.Time
		venuePreferredGameTime              *string
		venueLastAutoBookingAt              *time.Time
		venueAutoBookingCourts              *string
	)
	err := s.Scan(
		&g.ID, &g.ChatID, &g.MessageID, &g.GameDate, &g.CourtsCount, &g.Courts, &g.VenueID,
		&g.NotifiedDayBefore, &g.Completed, &g.CreatedAt,
		&venueID, &venueGroupID, &venueName, &venueCourts, &venueSlots, &venueAddr, &venueCreated,
		&venueGracePeriodHours, &venueGameDays, &venueBookingOpensDays, &venuePreventiveCancellationFraction, &venueLastBookingReminder,
		&venuePreferredGameTime, &venueLastAutoBookingAt, &venueAutoBookingCourts,
	)
	if err != nil {
		return nil, fmt.Errorf("scan game: %w", err)
	}
	if venueID != nil {
		addr := ""
		if venueAddr != nil {
			addr = *venueAddr
		}
		createdAt := time.Time{}
		if venueCreated != nil {
			createdAt = *venueCreated
		}
		gracePeriod := 24
		if venueGracePeriodHours != nil {
			gracePeriod = *venueGracePeriodHours
		}
		gameDays := ""
		if venueGameDays != nil {
			gameDays = *venueGameDays
		}
		bookingDays := 14
		if venueBookingOpensDays != nil {
			bookingDays = *venueBookingOpensDays
		}
		preventiveCancellationFraction := models.PreventiveCancellationFractionDefault
		if venuePreventiveCancellationFraction != nil {
			preventiveCancellationFraction = *venuePreventiveCancellationFraction
		}
		preferredGameTime := ""
		if venuePreferredGameTime != nil {
			preferredGameTime = *venuePreferredGameTime
		}
		autoBookingCourts := ""
		if venueAutoBookingCourts != nil {
			autoBookingCourts = *venueAutoBookingCourts
		}
		g.Venue = &models.Venue{
			ID:                             *venueID,
			GroupID:                        *venueGroupID,
			Name:                           *venueName,
			Courts:                         *venueCourts,
			TimeSlots:                      *venueSlots,
			Address:                        addr,
			CreatedAt:                      createdAt,
			GracePeriodHours:               gracePeriod,
			GameDays:                       gameDays,
			BookingOpensDays:               bookingDays,
			PreventiveCancellationFraction: preventiveCancellationFraction,
			LastBookingReminderAt:          venueLastBookingReminder,
			PreferredGameTimes:             preferredGameTime,
			LastAutoBookingAt:              venueLastAutoBookingAt,
			AutoBookingCourts:              autoBookingCourts,
		}
	}
	return &g, nil
}

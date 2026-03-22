package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type GameRepo struct {
	pool *pgxpool.Pool
}

func NewGameRepo(pool *pgxpool.Pool) *GameRepo {
	return &GameRepo{pool: pool}
}

func (r *GameRepo) Create(ctx context.Context, game *models.Game) (*models.Game, error) {
	const q = `
		INSERT INTO games (chat_id, game_date, courts_count, courts)
		VALUES ($1, $2, $3, $4)
		RETURNING id, chat_id, message_id, game_date, courts_count, courts,
		          notified_day_before, completed, created_at`

	slog.Debug("GameRepo.Create", "chat_id", game.ChatID, "game_date", game.GameDate)

	row := r.pool.QueryRow(ctx, q, game.ChatID, game.GameDate, game.CourtsCount, game.Courts)
	return scanGame(row)
}

func (r *GameRepo) GetByID(ctx context.Context, id int64) (*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts,
		       notified_day_before, completed, created_at
		FROM games WHERE id = $1`

	slog.Debug("GameRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanGame(row)
}

func (r *GameRepo) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	const q = `
		SELECT id, chat_id, message_id, game_date, courts_count, courts,
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
		SELECT id, chat_id, message_id, game_date, courts_count, courts,
		       notified_day_before, completed, created_at
		FROM games
		WHERE game_date >= $1 AND game_date < $2
		  AND notified_day_before = false`

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
		SELECT id, chat_id, message_id, game_date, courts_count, courts,
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

func (r *GameRepo) MarkCompleted(ctx context.Context, gameID int64) error {
	const q = `UPDATE games SET completed = true WHERE id = $1`
	slog.Debug("GameRepo.MarkCompleted", "game_id", gameID)
	_, err := r.pool.Exec(ctx, q, gameID)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGame(s scanner) (*models.Game, error) {
	var g models.Game
	err := s.Scan(
		&g.ID, &g.ChatID, &g.MessageID, &g.GameDate, &g.CourtsCount, &g.Courts,
		&g.NotifiedDayBefore, &g.Completed, &g.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan game: %w", err)
	}
	return &g, nil
}

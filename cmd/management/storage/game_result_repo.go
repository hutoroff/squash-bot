package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrGameResultNotPending is returned by Decide when the target row is not in pending status.
var ErrGameResultNotPending = errors.New("game result is not pending")

type GameResultRepo struct {
	pool *pgxpool.Pool
}

func NewGameResultRepo(pool *pgxpool.Pool) *GameResultRepo {
	return &GameResultRepo{pool: pool}
}

func (r *GameResultRepo) Create(ctx context.Context, res *models.GameResult) (int64, error) {
	const q = `
		INSERT INTO game_results
			(game_id, group_id, author_id, opponent_id, winner_id, score, status, submitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`
	var id int64
	err := r.pool.QueryRow(ctx, q,
		res.GameID, res.GroupID, res.AuthorID, res.OpponentID,
		res.WinnerID, res.Score, res.Status, res.SubmittedAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("game_result create: %w", err)
	}
	return id, nil
}

func (r *GameResultRepo) GetByID(ctx context.Context, id int64) (*models.GameResult, error) {
	const q = `
		SELECT id, game_id, group_id, author_id, opponent_id, winner_id, score,
		       status, submitted_at, decided_at, approval_chat_id, approval_message_id
		FROM game_results WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	res, err := scanGameResult(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return res, nil
}

func (r *GameResultRepo) SetApprovalMessage(ctx context.Context, id, chatID int64, messageID int) error {
	const q = `UPDATE game_results SET approval_chat_id = $1, approval_message_id = $2 WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, chatID, messageID, id)
	return err
}

func (r *GameResultRepo) Decide(ctx context.Context, id int64, status models.GameResultStatus, decidedAt time.Time) error {
	const q = `
		UPDATE game_results
		SET status = $1, decided_at = $2
		WHERE id = $3 AND status = 'pending'`
	tag, err := r.pool.Exec(ctx, q, status, decidedAt, id)
	if err != nil {
		return fmt.Errorf("game_result decide: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGameResultNotPending
	}
	return nil
}

func (r *GameResultRepo) ListPendingOlderThan(ctx context.Context, cutoff time.Time) ([]*models.GameResult, error) {
	const q = `
		SELECT id, game_id, group_id, author_id, opponent_id, winner_id, score,
		       status, submitted_at, decided_at, approval_chat_id, approval_message_id
		FROM game_results
		WHERE status = 'pending' AND submitted_at < $1
		ORDER BY submitted_at`
	rows, err := r.pool.Query(ctx, q, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list pending game results: %w", err)
	}
	defer rows.Close()
	return collectGameResults(rows)
}

func (r *GameResultRepo) ListByGroupAndDate(ctx context.Context, groupID int64, gameDate time.Time) ([]*models.GameResult, error) {
	const q = `
		SELECT gr.id, gr.game_id, gr.group_id, gr.author_id, gr.opponent_id, gr.winner_id, gr.score,
		       gr.status, gr.submitted_at, gr.decided_at, gr.approval_chat_id, gr.approval_message_id
		FROM game_results gr
		JOIN games g ON g.id = gr.game_id
		WHERE gr.group_id = $1
		  AND g.game_date::date = $2::date
		ORDER BY gr.submitted_at`
	rows, err := r.pool.Query(ctx, q, groupID, gameDate)
	if err != nil {
		return nil, fmt.Errorf("list game results by group and date: %w", err)
	}
	defer rows.Close()
	return collectGameResults(rows)
}

func (r *GameResultRepo) ListByGameID(ctx context.Context, gameID int64) ([]*models.GameResult, error) {
	const q = `
		SELECT id, game_id, group_id, author_id, opponent_id, winner_id, score,
		       status, submitted_at, decided_at, approval_chat_id, approval_message_id
		FROM game_results
		WHERE game_id = $1
		ORDER BY submitted_at`
	rows, err := r.pool.Query(ctx, q, gameID)
	if err != nil {
		return nil, fmt.Errorf("list game results by game: %w", err)
	}
	defer rows.Close()
	return collectGameResults(rows)
}

func scanGameResult(s scanner) (*models.GameResult, error) {
	var r models.GameResult
	err := s.Scan(
		&r.ID, &r.GameID, &r.GroupID, &r.AuthorID, &r.OpponentID,
		&r.WinnerID, &r.Score, &r.Status, &r.SubmittedAt,
		&r.DecidedAt, &r.ApprovalChatID, &r.ApprovalMessageID,
	)
	if err != nil {
		return nil, fmt.Errorf("scan game_result: %w", err)
	}
	return &r, nil
}

func collectGameResults(rows pgx.Rows) ([]*models.GameResult, error) {
	var results []*models.GameResult
	for rows.Next() {
		r, err := scanGameResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

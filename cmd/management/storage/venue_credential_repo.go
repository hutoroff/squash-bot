package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VenueCredentialRepo struct {
	pool *pgxpool.Pool
}

func NewVenueCredentialRepo(pool *pgxpool.Pool) *VenueCredentialRepo {
	return &VenueCredentialRepo{pool: pool}
}

func (r *VenueCredentialRepo) Create(ctx context.Context, venueID int64, login, encPassword string, priority int) (*models.VenueCredential, error) {
	const q = `
		INSERT INTO venue_credentials (venue_id, login, enc_password, priority)
		VALUES ($1, $2, $3, $4)
		RETURNING id, venue_id, login, priority, created_at`

	slog.Debug("VenueCredentialRepo.Create", "venue_id", venueID, "login", login, "priority", priority)

	var c models.VenueCredential
	err := r.pool.QueryRow(ctx, q, venueID, login, encPassword, priority).Scan(
		&c.ID, &c.VenueID, &c.Login, &c.Priority, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create venue credential: %w", err)
	}
	return &c, nil
}

func (r *VenueCredentialRepo) ListByVenueID(ctx context.Context, venueID int64) ([]*models.VenueCredential, error) {
	const q = `
		SELECT id, venue_id, login, priority, created_at
		FROM venue_credentials
		WHERE venue_id = $1
		ORDER BY priority ASC`

	slog.Debug("VenueCredentialRepo.ListByVenueID", "venue_id", venueID)

	rows, err := r.pool.Query(ctx, q, venueID)
	if err != nil {
		return nil, fmt.Errorf("list venue credentials: %w", err)
	}
	defer rows.Close()

	var creds []*models.VenueCredential
	for rows.Next() {
		var c models.VenueCredential
		if err := rows.Scan(&c.ID, &c.VenueID, &c.Login, &c.Priority, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan venue credential: %w", err)
		}
		creds = append(creds, &c)
	}
	return creds, rows.Err()
}

func (r *VenueCredentialRepo) Delete(ctx context.Context, id, venueID int64) error {
	const q = `DELETE FROM venue_credentials WHERE id = $1 AND venue_id = $2`
	slog.Debug("VenueCredentialRepo.Delete", "id", id, "venue_id", venueID)
	tag, err := r.pool.Exec(ctx, q, id, venueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credential not found")
	}
	return nil
}

func (r *VenueCredentialRepo) ExistsByLogin(ctx context.Context, venueID int64, login string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM venue_credentials WHERE venue_id = $1 AND login = $2)`
	slog.Debug("VenueCredentialRepo.ExistsByLogin", "venue_id", venueID, "login", login)
	var exists bool
	if err := r.pool.QueryRow(ctx, q, venueID, login).Scan(&exists); err != nil {
		return false, fmt.Errorf("check credential login existence: %w", err)
	}
	return exists, nil
}

func (r *VenueCredentialRepo) PrioritiesInUse(ctx context.Context, venueID int64) ([]int, error) {
	const q = `SELECT priority FROM venue_credentials WHERE venue_id = $1 ORDER BY priority ASC`
	slog.Debug("VenueCredentialRepo.PrioritiesInUse", "venue_id", venueID)
	rows, err := r.pool.Query(ctx, q, venueID)
	if err != nil {
		return nil, fmt.Errorf("list priorities: %w", err)
	}
	defer rows.Close()

	var priorities []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan priority: %w", err)
		}
		priorities = append(priorities, p)
	}
	return priorities, rows.Err()
}

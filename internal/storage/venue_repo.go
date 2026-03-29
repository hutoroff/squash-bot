package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type VenueRepo struct {
	pool *pgxpool.Pool
}

func NewVenueRepo(pool *pgxpool.Pool) *VenueRepo {
	return &VenueRepo{pool: pool}
}

func (r *VenueRepo) Create(ctx context.Context, venue *models.Venue) (*models.Venue, error) {
	const q = `
		INSERT INTO venues (group_id, name, courts, time_slots, address)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at`

	slog.Debug("VenueRepo.Create", "group_id", venue.GroupID, "name", venue.Name)

	row := r.pool.QueryRow(ctx, q, venue.GroupID, venue.Name, venue.Courts, venue.TimeSlots, nullableText(venue.Address))
	return scanVenue(row)
}

func (r *VenueRepo) GetByID(ctx context.Context, id int64) (*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at
		FROM venues WHERE id = $1`

	slog.Debug("VenueRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanVenue(row)
}

// GetByIDAndGroupID fetches a venue only if it belongs to the given group (ownership check).
func (r *VenueRepo) GetByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at
		FROM venues WHERE id = $1 AND group_id = $2`

	slog.Debug("VenueRepo.GetByIDAndGroupID", "id", id, "group_id", groupID)

	row := r.pool.QueryRow(ctx, q, id, groupID)
	return scanVenue(row)
}

func (r *VenueRepo) GetByGroupID(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at
		FROM venues WHERE group_id = $1 ORDER BY name`

	slog.Debug("VenueRepo.GetByGroupID", "group_id", groupID)

	rows, err := r.pool.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("query venues by group: %w", err)
	}
	defer rows.Close()

	var venues []*models.Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, err
		}
		venues = append(venues, v)
	}
	return venues, rows.Err()
}

func (r *VenueRepo) Update(ctx context.Context, venue *models.Venue) (*models.Venue, error) {
	const q = `
		UPDATE venues
		SET name = $1, courts = $2, time_slots = $3, address = $4
		WHERE id = $5 AND group_id = $6
		RETURNING id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at`

	slog.Debug("VenueRepo.Update", "id", venue.ID, "group_id", venue.GroupID)

	row := r.pool.QueryRow(ctx, q,
		venue.Name, venue.Courts, venue.TimeSlots, nullableText(venue.Address),
		venue.ID, venue.GroupID,
	)
	return scanVenue(row)
}

// Delete removes a venue. It is scoped to groupID to prevent cross-group deletions.
func (r *VenueRepo) Delete(ctx context.Context, id, groupID int64) error {
	const q = `DELETE FROM venues WHERE id = $1 AND group_id = $2`
	slog.Debug("VenueRepo.Delete", "id", id, "group_id", groupID)
	tag, err := r.pool.Exec(ctx, q, id, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("venue not found")
	}
	return nil
}

func scanVenue(s scanner) (*models.Venue, error) {
	var v models.Venue
	err := s.Scan(
		&v.ID, &v.GroupID, &v.Name, &v.Courts, &v.TimeSlots, &v.Address, &v.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan venue: %w", err)
	}
	return &v, nil
}

// nullableText converts empty string to nil for nullable TEXT columns.
func nullableText(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

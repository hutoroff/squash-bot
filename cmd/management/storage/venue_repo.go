package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VenueRepo struct {
	pool *pgxpool.Pool
}

func NewVenueRepo(pool *pgxpool.Pool) *VenueRepo {
	return &VenueRepo{pool: pool}
}

func (r *VenueRepo) Create(ctx context.Context, venue *models.Venue) (*models.Venue, error) {
	const q = `
		INSERT INTO venues (group_id, name, courts, time_slots, address, grace_period_hours, game_days, booking_opens_days, preferred_game_times, auto_booking_courts, auto_booking_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at,
		          grace_period_hours, game_days, booking_opens_days, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled`

	slog.Debug("VenueRepo.Create", "group_id", venue.GroupID, "name", venue.Name)

	row := r.pool.QueryRow(ctx, q,
		venue.GroupID, venue.Name, venue.Courts, venue.TimeSlots, nullableText(venue.Address),
		venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, venue.PreferredGameTimes,
		venue.AutoBookingCourts, venue.AutoBookingEnabled,
	)
	return scanVenue(row)
}

func (r *VenueRepo) GetByID(ctx context.Context, id int64) (*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at,
		       grace_period_hours, game_days, booking_opens_days, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled
		FROM venues WHERE id = $1`

	slog.Debug("VenueRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanVenue(row)
}

// GetByIDAndGroupID fetches a venue only if it belongs to the given group (ownership check).
func (r *VenueRepo) GetByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at,
		       grace_period_hours, game_days, booking_opens_days, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled
		FROM venues WHERE id = $1 AND group_id = $2`

	slog.Debug("VenueRepo.GetByIDAndGroupID", "id", id, "group_id", groupID)

	row := r.pool.QueryRow(ctx, q, id, groupID)
	return scanVenue(row)
}

func (r *VenueRepo) GetByGroupID(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	const q = `
		SELECT id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at,
		       grace_period_hours, game_days, booking_opens_days, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled
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
		SET name = $1, courts = $2, time_slots = $3, address = $4,
		    grace_period_hours = $5, game_days = $6, booking_opens_days = $7,
		    preferred_game_times = $8, auto_booking_courts = $9, auto_booking_enabled = $10
		WHERE id = $11 AND group_id = $12
		RETURNING id, group_id, name, courts, time_slots, COALESCE(address, ''), created_at,
		          grace_period_hours, game_days, booking_opens_days, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled`

	slog.Debug("VenueRepo.Update", "id", venue.ID, "group_id", venue.GroupID)

	row := r.pool.QueryRow(ctx, q,
		venue.Name, venue.Courts, venue.TimeSlots, nullableText(venue.Address),
		venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays,
		venue.PreferredGameTimes, venue.AutoBookingCourts, venue.AutoBookingEnabled,
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

// SetLastBookingReminderAt marks the booking reminder as sent now for a venue.
func (r *VenueRepo) SetLastBookingReminderAt(ctx context.Context, venueID int64) error {
	const q = `UPDATE venues SET last_booking_reminder_at = now() WHERE id = $1`
	slog.Debug("VenueRepo.SetLastBookingReminderAt", "venue_id", venueID)
	_, err := r.pool.Exec(ctx, q, venueID)
	return err
}

// SetLastAutoBookingAt marks the auto-booking as performed now for a venue.
func (r *VenueRepo) SetLastAutoBookingAt(ctx context.Context, venueID int64) error {
	const q = `UPDATE venues SET last_auto_booking_at = now() WHERE id = $1`
	slog.Debug("VenueRepo.SetLastAutoBookingAt", "venue_id", venueID)
	_, err := r.pool.Exec(ctx, q, venueID)
	return err
}

func scanVenue(s scanner) (*models.Venue, error) {
	var v models.Venue
	err := s.Scan(
		&v.ID, &v.GroupID, &v.Name, &v.Courts, &v.TimeSlots, &v.Address, &v.CreatedAt,
		&v.GracePeriodHours, &v.GameDays, &v.BookingOpensDays, &v.LastBookingReminderAt,
		&v.PreferredGameTimes, &v.LastAutoBookingAt, &v.AutoBookingCourts, &v.AutoBookingEnabled,
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

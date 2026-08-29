package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/sport"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VenueRepo struct {
	pool *pgxpool.Pool
}

func NewVenueRepo(pool *pgxpool.Pool) *VenueRepo {
	return &VenueRepo{pool: pool}
}

func (r *VenueRepo) Create(ctx context.Context, venue *models.Venue) (*models.Venue, error) {
	if len(venue.Sports) == 0 {
		venue.Sports = []models.VenueSport{{Sport: string(sport.Default), Courts: venue.Courts}}
	}
	const q = `
		INSERT INTO venues (group_id, name, time_slots, address, grace_period_hours, game_days, booking_opens_days, preventive_cancellation_fraction, preferred_game_times, auto_booking_courts, auto_booking_enabled, auto_booking_courts_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, '1/2'), $9, $10, $11, $12)
		RETURNING id`

	slog.Debug("VenueRepo.Create", "group_id", venue.GroupID, "name", venue.Name)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	err = tx.QueryRow(ctx, q,
		venue.GroupID, venue.Name, venue.TimeSlots, nullableText(venue.Address),
		venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, nullableText(venue.PreventiveCancellationFraction), venue.PreferredGameTimes,
		venue.AutoBookingCourts, venue.AutoBookingEnabled, venue.AutoBookingCourtsCount,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := insertVenueSports(ctx, tx, id, venue.Sports); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r *VenueRepo) GetByID(ctx context.Context, id int64) (*models.Venue, error) {
	const q = `
		SELECT v.id, v.group_id, v.name,
		       COALESCE((SELECT courts FROM venue_sports WHERE venue_id = v.id AND sport = 'squash'), ''),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('sport', sport, 'courts', courts, 'players_per_court', players_per_court) ORDER BY sport) FROM venue_sports WHERE venue_id = v.id), '[]'),
		       v.time_slots, COALESCE(v.address, ''), v.created_at,
		       grace_period_hours, game_days, booking_opens_days, preventive_cancellation_fraction, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled, auto_booking_courts_count
		FROM venues v WHERE v.id = $1`

	slog.Debug("VenueRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanVenue(row)
}

// GetByIDAndGroupID fetches a venue only if it belongs to the given group (ownership check).
func (r *VenueRepo) GetByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error) {
	const q = `
		SELECT v.id, v.group_id, v.name,
		       COALESCE((SELECT courts FROM venue_sports WHERE venue_id = v.id AND sport = 'squash'), ''),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('sport', sport, 'courts', courts, 'players_per_court', players_per_court) ORDER BY sport) FROM venue_sports WHERE venue_id = v.id), '[]'),
		       v.time_slots, COALESCE(v.address, ''), v.created_at,
		       grace_period_hours, game_days, booking_opens_days, preventive_cancellation_fraction, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled, auto_booking_courts_count
		FROM venues v WHERE v.id = $1 AND v.group_id = $2`

	slog.Debug("VenueRepo.GetByIDAndGroupID", "id", id, "group_id", groupID)

	row := r.pool.QueryRow(ctx, q, id, groupID)
	return scanVenue(row)
}

func (r *VenueRepo) GetByGroupID(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	const q = `
		SELECT v.id, v.group_id, v.name,
		       COALESCE((SELECT courts FROM venue_sports WHERE venue_id = v.id AND sport = 'squash'), ''),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('sport', sport, 'courts', courts, 'players_per_court', players_per_court) ORDER BY sport) FROM venue_sports WHERE venue_id = v.id), '[]'),
		       v.time_slots, COALESCE(v.address, ''), v.created_at,
		       grace_period_hours, game_days, booking_opens_days, preventive_cancellation_fraction, last_booking_reminder_at, preferred_game_times, last_auto_booking_at, auto_booking_courts, auto_booking_enabled, auto_booking_courts_count
		FROM venues v WHERE v.group_id = $1 ORDER BY v.name`

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
	if len(venue.Sports) == 0 {
		venue.Sports = []models.VenueSport{{Sport: string(sport.Default), Courts: venue.Courts}}
	}
	const q = `
		UPDATE venues
		SET name = $1, time_slots = $2, address = $3,
		    grace_period_hours = $4, game_days = $5, booking_opens_days = $6,
		    preventive_cancellation_fraction = COALESCE($7, preventive_cancellation_fraction),
		    preferred_game_times = $8, auto_booking_courts = $9, auto_booking_enabled = $10,
		    auto_booking_courts_count = $11
		WHERE id = $12 AND group_id = $13`

	slog.Debug("VenueRepo.Update", "id", venue.ID, "group_id", venue.GroupID)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, q,
		venue.Name, venue.TimeSlots, nullableText(venue.Address),
		venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, nullableText(venue.PreventiveCancellationFraction),
		venue.PreferredGameTimes, venue.AutoBookingCourts, venue.AutoBookingEnabled,
		venue.AutoBookingCourtsCount, venue.ID, venue.GroupID,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM venue_sports WHERE venue_id = $1`, venue.ID); err != nil {
		return nil, err
	}
	if err := insertVenueSports(ctx, tx, venue.ID, venue.Sports); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, venue.ID)
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
		&v.ID, &v.GroupID, &v.Name, &v.Courts, &v.Sports, &v.TimeSlots, &v.Address, &v.CreatedAt,
		&v.GracePeriodHours, &v.GameDays, &v.BookingOpensDays, &v.PreventiveCancellationFraction, &v.LastBookingReminderAt,
		&v.PreferredGameTimes, &v.LastAutoBookingAt, &v.AutoBookingCourts, &v.AutoBookingEnabled,
		&v.AutoBookingCourtsCount,
	)
	if err != nil {
		return nil, fmt.Errorf("scan venue: %w", err)
	}
	return &v, nil
}

func insertVenueSports(ctx context.Context, tx pgx.Tx, venueID int64, sports []models.VenueSport) error {
	for _, venueSport := range sports {
		if _, err := tx.Exec(ctx, `INSERT INTO venue_sports (venue_id, sport, courts, players_per_court) VALUES ($1, $2, $3, $4)`, venueID, venueSport.Sport, venueSport.Courts, venueSport.PlayersPerCourt); err != nil {
			return err
		}
	}
	return nil
}

// nullableText converts empty string to nil for nullable TEXT columns.
func nullableText(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

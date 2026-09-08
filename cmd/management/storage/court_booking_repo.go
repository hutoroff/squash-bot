package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CourtBookingRepo struct {
	pool *pgxpool.Pool
}

func NewCourtBookingRepo(pool *pgxpool.Pool) *CourtBookingRepo {
	return &CourtBookingRepo{pool: pool}
}

func (r *CourtBookingRepo) Save(ctx context.Context, b *models.CourtBooking) error {
	const q = `
		INSERT INTO court_bookings (
			venue_id, game_date, game_time, court_uuid, court_label, match_id, booking_uuid, credential_id,
			management_create_operation_id, booking_service_key, booking_provider, booking_facility_id,
			booking_facility_uuid, booking_facility_slug, booking_sport
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''))
		ON CONFLICT (match_id) DO UPDATE SET
			credential_id = COALESCE(court_bookings.credential_id, EXCLUDED.credential_id),
			management_create_operation_id = COALESCE(court_bookings.management_create_operation_id, EXCLUDED.management_create_operation_id),
			booking_service_key = COALESCE(court_bookings.booking_service_key, EXCLUDED.booking_service_key),
			booking_provider = COALESCE(court_bookings.booking_provider, EXCLUDED.booking_provider),
			booking_facility_id = COALESCE(court_bookings.booking_facility_id, EXCLUDED.booking_facility_id),
			booking_facility_uuid = COALESCE(court_bookings.booking_facility_uuid, EXCLUDED.booking_facility_uuid),
			booking_facility_slug = COALESCE(court_bookings.booking_facility_slug, EXCLUDED.booking_facility_slug),
			booking_sport = COALESCE(court_bookings.booking_sport, EXCLUDED.booking_sport)
		WHERE court_bookings.venue_id = EXCLUDED.venue_id
		  AND court_bookings.game_date = EXCLUDED.game_date
		  AND court_bookings.game_time = EXCLUDED.game_time
		  AND court_bookings.court_uuid = EXCLUDED.court_uuid
		  AND court_bookings.booking_uuid = EXCLUDED.booking_uuid
		  AND (court_bookings.management_create_operation_id IS NULL OR court_bookings.management_create_operation_id = EXCLUDED.management_create_operation_id)`
	tag, err := r.pool.Exec(ctx, q,
		b.VenueID, b.GameDate, b.GameTime, b.CourtUUID, b.CourtLabel, b.MatchID, b.BookingUUID, b.CredentialID,
		b.CreateOperationID, b.OriginScope.ServiceKey, b.OriginScope.Provider, b.OriginScope.FacilityID,
		b.OriginScope.FacilityUUID, b.OriginScope.FacilitySlug, b.OriginScope.Sport)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("court booking match ID conflicts with different durable booking evidence")
	}
	return nil
}

// GetByVenueAndDate returns active (non-canceled) bookings for the venue and date.
func (r *CourtBookingRepo) GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.CourtBooking, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, court_uuid, court_label, match_id, booking_uuid,
		       credential_id, COALESCE(management_create_operation_id::text,''),
		       COALESCE(booking_service_key,''), COALESCE(booking_provider,''), COALESCE(booking_facility_id,''),
		       COALESCE(booking_facility_uuid,''), COALESCE(booking_facility_slug,''), COALESCE(booking_sport,''),
		       canceled_at, created_at
		FROM court_bookings
		WHERE venue_id = $1 AND game_date = $2 AND canceled_at IS NULL
		ORDER BY id ASC`
	rows, err := r.pool.Query(ctx, q, venueID, gameDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*models.CourtBooking
	for rows.Next() {
		booking, err := scanCourtBooking(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, booking)
	}
	return result, rows.Err()
}

// GetByVenueAndDateAndTime returns active (non-canceled) bookings for the venue, date, and
// specific game time slot. Falls back to rows with game_time=” (legacy) when gameTime is non-empty
// and no time-specific rows exist.
func (r *CourtBookingRepo) GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) ([]*models.CourtBooking, error) {
	result, err := r.queryBookingsByTime(ctx, venueID, gameDate, gameTime)
	if err != nil || len(result) > 0 || gameTime == "" {
		return result, err
	}
	// Legacy fallback: no rows for the requested time — try empty game_time (pre-migration rows).
	return r.queryBookingsByTime(ctx, venueID, gameDate, "")
}

func (r *CourtBookingRepo) queryBookingsByTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) ([]*models.CourtBooking, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, court_uuid, court_label, match_id, booking_uuid,
		       credential_id, COALESCE(management_create_operation_id::text,''),
		       COALESCE(booking_service_key,''), COALESCE(booking_provider,''), COALESCE(booking_facility_id,''),
		       COALESCE(booking_facility_uuid,''), COALESCE(booking_facility_slug,''), COALESCE(booking_sport,''),
		       canceled_at, created_at
		FROM court_bookings
		WHERE venue_id = $1 AND game_date = $2 AND game_time = $3 AND canceled_at IS NULL
		ORDER BY id ASC`
	rows, err := r.pool.Query(ctx, q, venueID, gameDate, gameTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*models.CourtBooking
	for rows.Next() {
		booking, err := scanCourtBooking(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, booking)
	}
	return result, rows.Err()
}

// MarkCanceled soft-deletes a court booking by setting canceled_at to the current time.
func (r *CourtBookingRepo) MarkCanceled(ctx context.Context, matchID string) error {
	const q = `UPDATE court_bookings SET canceled_at = NOW() WHERE match_id = $1 AND canceled_at IS NULL`
	_, err := r.pool.Exec(ctx, q, matchID)
	return err
}

// HasActiveByCredentialID returns true for a non-canceled current/future court booking or any unresolved management booking operation using the credential.
func (r *CourtBookingRepo) HasActiveByCredentialID(ctx context.Context, credentialID int64) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1
			FROM court_bookings cb
			JOIN venues v ON v.id = cb.venue_id
			JOIN bot_groups bg ON bg.chat_id = v.group_id
			WHERE cb.credential_id = $1
				AND cb.canceled_at IS NULL
				AND cb.game_date >= (NOW() AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
			UNION ALL
			SELECT 1
			FROM management_booking_operations mbo
			WHERE mbo.credential_id = $1
				AND mbo.state IN ('planned', 'accepted', 'running', 'outcome_unknown')
		)`
	var exists bool
	err := r.pool.QueryRow(ctx, q, credentialID).Scan(&exists)
	return exists, err
}

// HasActiveByVenueID returns true for a non-canceled current/future court booking or any unresolved management booking operation for the venue.
func (r *CourtBookingRepo) HasActiveByVenueID(ctx context.Context, venueID int64) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1
			FROM court_bookings cb
			JOIN venues v ON v.id = cb.venue_id
			JOIN bot_groups bg ON bg.chat_id = v.group_id
			WHERE cb.venue_id = $1
				AND cb.canceled_at IS NULL
				AND cb.game_date >= (NOW() AT TIME ZONE COALESCE(NULLIF(bg.timezone, ''), 'UTC'))::date
			UNION ALL
			SELECT 1
			FROM management_booking_intents mbi
			WHERE mbi.venue_id = $1
				AND mbi.state IN ('planned', 'active', 'unknown')
		)`
	var exists bool
	err := r.pool.QueryRow(ctx, q, venueID).Scan(&exists)
	return exists, err
}

// GetActiveByVenueDateAndLabels returns active bookings for the venue+date whose court_label
// matches one of the provided labels. Used by the manual court-removal flow.
func (r *CourtBookingRepo) GetActiveByVenueDateAndLabels(ctx context.Context, venueID int64, gameDate time.Time, labels []string) ([]*models.CourtBooking, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, court_uuid, court_label, match_id, booking_uuid,
		       credential_id, COALESCE(management_create_operation_id::text,''),
		       COALESCE(booking_service_key,''), COALESCE(booking_provider,''), COALESCE(booking_facility_id,''),
		       COALESCE(booking_facility_uuid,''), COALESCE(booking_facility_slug,''), COALESCE(booking_sport,''),
		       canceled_at, created_at
		FROM court_bookings
		WHERE venue_id = $1 AND game_date = $2 AND court_label = ANY($3) AND canceled_at IS NULL
		ORDER BY id ASC`
	rows, err := r.pool.Query(ctx, q, venueID, gameDate, labels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*models.CourtBooking
	for rows.Next() {
		booking, err := scanCourtBooking(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, booking)
	}
	return result, rows.Err()
}

type courtBookingScanner interface {
	Scan(...any) error
}

func scanCourtBooking(scanner courtBookingScanner) (*models.CourtBooking, error) {
	var booking models.CourtBooking
	err := scanner.Scan(
		&booking.ID, &booking.VenueID, &booking.GameDate, &booking.GameTime, &booking.CourtUUID, &booking.CourtLabel,
		&booking.MatchID, &booking.BookingUUID, &booking.CredentialID, &booking.CreateOperationID,
		&booking.OriginScope.ServiceKey, &booking.OriginScope.Provider, &booking.OriginScope.FacilityID,
		&booking.OriginScope.FacilityUUID, &booking.OriginScope.FacilitySlug, &booking.OriginScope.Sport,
		&booking.CanceledAt, &booking.CreatedAt,
	)
	return &booking, err
}

// MarkCanceledByVenueAndDate soft-deletes all active bookings for the venue on the given date.
func (r *CourtBookingRepo) MarkCanceledByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) error {
	const q = `UPDATE court_bookings SET canceled_at = NOW() WHERE venue_id = $1 AND game_date = $2 AND canceled_at IS NULL`
	_, err := r.pool.Exec(ctx, q, venueID, gameDate)
	return err
}

package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	bookingmigrations "github.com/hutoroff/squash-bot/cmd/booking/storage/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
)

const (
	migrationHistoryTable = "booking_schema_migrations"
	defaultMaxConnections = int32(5)
	identityKeyHexLength  = 64
)

func DecodeIdentityKey(value string) ([]byte, error) {
	if len(value) != identityKeyHexLength {
		return nil, fmt.Errorf("BOOKING_IDENTITY_KEY must be exactly %d hexadecimal characters", identityKeyHexLength)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("BOOKING_IDENTITY_KEY must be hexadecimal: %w", err)
	}
	return decoded, nil
}

func IdentityKeyMarker(key []byte) (string, error) {
	if len(key) != identityKeyHexLength/2 {
		return "", fmt.Errorf("decoded BOOKING_IDENTITY_KEY must be exactly %d bytes", identityKeyHexLength/2)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("squash-booking-identity-key:v1"))
	return "hmac-sha256:v1:" + hex.EncodeToString(mac.Sum(nil)), nil
}

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse booking database URL: %w", err)
	}
	cfg.MaxConns = defaultMaxConnections

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create booking database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping booking database: %w", err)
	}
	return pool, nil
}

// RunMigrations applies only the booking-owned migration stream. Its migration
// table name is also part of golang-migrate's PostgreSQL advisory-lock ID, so it
// does not share migration history or a lock namespace with management.
func RunMigrations(databaseURL string) (err error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("open booking migration database: %w", err)
	}

	driver, err := migratepostgres.WithInstance(db, &migratepostgres.Config{
		MigrationsTable:  migrationHistoryTable,
		StatementTimeout: 30 * time.Second,
	})
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("create booking migration driver: %w", err)
	}
	source, err := iofs.New(bookingmigrations.FS, ".")
	if err != nil {
		_ = driver.Close()
		return fmt.Errorf("create booking migration source: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "booking-postgres", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return fmt.Errorf("create booking migrator: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		if err == nil && (sourceErr != nil || databaseErr != nil) {
			err = fmt.Errorf("close booking migrator: source=%v database=%v", sourceErr, databaseErr)
		}
	}()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply booking migrations: %w", err)
	}
	return nil
}

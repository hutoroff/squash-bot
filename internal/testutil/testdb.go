package testutil

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/hutoroff/squash-bot/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SetupTestDB starts a PostgreSQL container, runs all migrations, and returns
// a connected pool and a cleanup function to call when tests finish.
func SetupTestDB(ctx context.Context) (pool *pgxpool.Pool, cleanup func(), err error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "squash_test",
			"POSTGRES_USER":     "squash_test",
			"POSTGRES_PASSWORD": "squash_test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(60 * time.Second),
	}

	container, startErr := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if startErr != nil {
		return nil, nil, fmt.Errorf("start postgres container: %w", startErr)
	}

	host, hostErr := container.Host(ctx)
	if hostErr != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("get container host: %w", hostErr)
	}

	port, portErr := container.MappedPort(ctx, "5432")
	if portErr != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("get container port: %w", portErr)
	}

	dsn := fmt.Sprintf("postgres://squash_test:squash_test@%s:%s/squash_test?sslmode=disable",
		host, port.Port())

	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("create pool: %w", err)
	}

	if err = RunMigrations(dsn); err != nil {
		pool.Close()
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}

	cleanup = func() {
		pool.Close()
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("terminate test container: %v", termErr)
		}
	}
	return pool, cleanup, nil
}

// RunMigrations applies all database migrations to the given database URL.
// Uses the same embedded SQL files and driver as the production startup.
func RunMigrations(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// CheckDocker reports whether the Docker CLI and daemon are available.
// Docker-backed suites call this from TestMain so an explicitly requested
// integration run fails instead of silently succeeding without tests.
func CheckDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("Docker CLI not found: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Docker daemon check timed out: %w", ctx.Err())
		}
		return fmt.Errorf("Docker daemon is not reachable: %w", err)
	}
	return nil
}

// Truncate removes all rows from every table and resets serial sequences.
// Call this at the start of each test to ensure isolation.
func Truncate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		"TRUNCATE rating_changes, player_ratings, game_results, audit_events, guest_participations, game_participations, players, games, venue_sports, venues, bot_groups, user_identities, users RESTART IDENTITY CASCADE")
	return err
}

// CreateTestUser inserts a user with a telegram identity for test fixtures
// and returns the new user ID. username may be empty.
func CreateTestUser(ctx context.Context, pool *pgxpool.Pool, telegramID int64, username string) (int64, error) {
	displayName := ""
	if username != "" {
		displayName = "@" + username
	}
	var userID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (display_name) VALUES ($1) RETURNING id", displayName,
	).Scan(&userID); err != nil {
		return 0, fmt.Errorf("insert test user: %w", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO user_identities (user_id, provider, external_id, username) VALUES ($1, 'telegram', $2, NULLIF($3, ''))",
		userID, fmt.Sprintf("%d", telegramID), username,
	); err != nil {
		return 0, fmt.Errorf("insert test identity: %w", err)
	}
	return userID, nil
}

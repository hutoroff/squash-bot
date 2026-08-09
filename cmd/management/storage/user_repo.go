package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLastServerOwner is returned by SetServerOwner when revoking would leave
// the system with no server owner.
var ErrLastServerOwner = errors.New("cannot revoke the last server owner")

// errIdentityRace signals that a concurrent first-resolve of the same
// identity won the create race; the caller should retry, which will then
// find the row the winner just committed.
var errIdentityRace = errors.New("identity race: retry")

// serverOwnerLockKey serializes server-owner revocations so the last-owner
// guard can't race (two concurrent revocations could otherwise each observe
// count > 1 and both succeed, leaving zero owners). Arbitrary constant.
const serverOwnerLockKey = 727001

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

// UserSummary is a User annotated with the identity providers linked to it;
// used by the owner-only users list.
type UserSummary struct {
	models.User
	Providers []string `json:"providers"`
}

const userColumns = "id, display_name, is_server_owner, dm_language, results_opt_out, created_at, updated_at"

// findOrCreateUserID returns the user_id for an existing (provider,
// external_id) identity, or creates a blank user + identity row when none
// exists. Returns errIdentityRace if a concurrent transaction won the create
// race — the caller should retry, which will then find the winner's row.
func findOrCreateUserID(ctx context.Context, tx pgx.Tx, provider, externalID, displayNameForNew string) (int64, error) {
	var userID int64
	const selectIdentity = `SELECT user_id FROM user_identities WHERE provider = $1 AND external_id = $2`
	err := tx.QueryRow(ctx, selectIdentity, provider, externalID).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("select identity: %w", err)
	}

	const insertUser = `INSERT INTO users (display_name) VALUES ($1) RETURNING id`
	if err := tx.QueryRow(ctx, insertUser, displayNameForNew).Scan(&userID); err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	const insertIdentity = `INSERT INTO user_identities (user_id, provider, external_id) VALUES ($1, $2, $3)`
	if _, err := tx.Exec(ctx, insertIdentity, userID, provider, externalID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, errIdentityRace
		}
		return 0, fmt.Errorf("insert identity: %w", err)
	}
	return userID, nil
}

// ResolveIdentity finds-or-creates the user for the given (provider,
// external_id) and overwrites the identity's profile with the caller's
// current snapshot: username/first_name/last_name are always overwritten
// (Telegram always reports these accurately, so an empty value means the
// user genuinely cleared that field — treating it as "unknown" would pin a
// stale display name forever). photo_url is the one exception: it's only
// overwritten when non-empty, since the bot's message updates never carry a
// photo URL at all (empty there means "this caller has no opinion", not
// "cleared"). display_name is recomputed from the resulting profile.
//
// Race-safe: concurrent first-resolves of the same identity race on the
// user_identities unique constraint; the loser retries and takes the update path.
func (r *UserRepo) ResolveIdentity(ctx context.Context, provider, externalID, username, firstName, lastName, photoURL string) (*models.User, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		user, retry, err := r.resolveIdentityOnce(ctx, provider, externalID, username, firstName, lastName, photoURL)
		if !retry {
			return user, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("resolve identity: exhausted retries: %w", lastErr)
}

func (r *UserRepo) resolveIdentityOnce(ctx context.Context, provider, externalID, username, firstName, lastName, photoURL string) (user *models.User, retry bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	userID, err := findOrCreateUserID(ctx, tx, provider, externalID, displayNameFor(username, firstName, lastName))
	if errors.Is(err, errIdentityRace) {
		return nil, true, err
	}
	if err != nil {
		return nil, false, err
	}

	const updateIdentity = `
		UPDATE user_identities
		SET username   = NULLIF($3, ''),
		    first_name = NULLIF($4, ''),
		    last_name  = NULLIF($5, ''),
		    photo_url  = COALESCE(NULLIF($6, ''), photo_url),
		    updated_at = NOW()
		WHERE provider = $1 AND external_id = $2`
	if _, err := tx.Exec(ctx, updateIdentity, provider, externalID, username, firstName, lastName, photoURL); err != nil {
		return nil, false, fmt.Errorf("update identity: %w", err)
	}

	displayName := displayNameFor(username, firstName, lastName)
	u := &models.User{}
	const updateUser = `
		UPDATE users SET display_name = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING ` + userColumns
	if err := tx.QueryRow(ctx, updateUser, userID, displayName).Scan(
		&u.ID, &u.DisplayName, &u.IsServerOwner, &u.DMLanguage, &u.ResultsOptOut, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return nil, false, fmt.Errorf("update display name: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit tx: %w", err)
	}
	return u, false, nil
}

// EnsureIdentity finds-or-creates the user for the given (provider,
// external_id) WITHOUT touching any existing profile data or display_name.
// Used only by the server-owner seed, which has no fresh profile to offer —
// unlike ResolveIdentity, it must never overwrite real data acquired from an
// actual login/interaction with blanks on every restart.
func (r *UserRepo) EnsureIdentity(ctx context.Context, provider, externalID string) (*models.User, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		user, retry, err := r.ensureIdentityOnce(ctx, provider, externalID)
		if !retry {
			return user, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("ensure identity: exhausted retries: %w", lastErr)
}

func (r *UserRepo) ensureIdentityOnce(ctx context.Context, provider, externalID string) (user *models.User, retry bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	userID, err := findOrCreateUserID(ctx, tx, provider, externalID, "")
	if errors.Is(err, errIdentityRace) {
		return nil, true, err
	}
	if err != nil {
		return nil, false, err
	}

	u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit tx: %w", err)
	}
	return u, false, nil
}

// GrantServerOwnersByTelegramID sets is_server_owner = TRUE for the given
// Telegram IDs, creating the user/identity if it doesn't exist yet. It never
// touches an existing identity's profile (the seed has none to offer) and
// never revokes — removing an ID from the seed list has no effect.
func (r *UserRepo) GrantServerOwnersByTelegramID(ctx context.Context, telegramIDs []int64) error {
	for _, tgID := range telegramIDs {
		user, err := r.EnsureIdentity(ctx, models.IdentityProviderTelegram, strconv.FormatInt(tgID, 10))
		if err != nil {
			return fmt.Errorf("resolve telegram id %d: %w", tgID, err)
		}
		if user.IsServerOwner {
			continue
		}
		if _, err := r.pool.Exec(ctx, `UPDATE users SET is_server_owner = TRUE, updated_at = NOW() WHERE id = $1`, user.ID); err != nil {
			return fmt.Errorf("grant owner to user %d: %w", user.ID, err)
		}
	}
	return nil
}

func (r *UserRepo) GetByID(ctx context.Context, userID int64) (*models.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	return scanUser(r.pool.QueryRow(ctx, q, userID))
}

// List returns every user with the providers linked to it, newest first.
func (r *UserRepo) List(ctx context.Context) ([]*UserSummary, error) {
	const q = `
		SELECT u.id, u.display_name, u.is_server_owner, u.dm_language, u.results_opt_out,
		       u.created_at, u.updated_at,
		       COALESCE(array_agg(ui.provider) FILTER (WHERE ui.provider IS NOT NULL), '{}')
		FROM users u
		LEFT JOIN user_identities ui ON ui.user_id = u.id
		GROUP BY u.id
		ORDER BY u.id DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []*UserSummary
	for rows.Next() {
		var s UserSummary
		if err := rows.Scan(
			&s.ID, &s.DisplayName, &s.IsServerOwner, &s.DMLanguage, &s.ResultsOptOut, &s.CreatedAt, &s.UpdatedAt,
			&s.Providers,
		); err != nil {
			return nil, fmt.Errorf("scan user summary: %w", err)
		}
		summaries = append(summaries, &s)
	}
	return summaries, rows.Err()
}

// SetServerOwner grants or revokes the server-owner role. Revoking the last
// remaining owner returns ErrLastServerOwner. Revocations are serialized via
// an advisory lock so two concurrent revocations can't each observe
// count > 1 and both succeed, leaving zero owners.
func (r *UserRepo) SetServerOwner(ctx context.Context, userID int64, enabled bool) error {
	if enabled {
		tag, err := r.pool.Exec(ctx, `UPDATE users SET is_server_owner = TRUE, updated_at = NOW() WHERE id = $1`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, serverOwnerLockKey); err != nil {
		return fmt.Errorf("acquire owner lock: %w", err)
	}

	var isOwner bool
	err = tx.QueryRow(ctx, `SELECT is_server_owner FROM users WHERE id = $1`, userID).Scan(&isOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("check target: %w", err)
	}
	if !isOwner {
		return pgx.ErrNoRows
	}

	var ownerCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_server_owner`).Scan(&ownerCount); err != nil {
		return fmt.Errorf("count owners: %w", err)
	}
	if ownerCount <= 1 {
		return ErrLastServerOwner
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET is_server_owner = FALSE, updated_at = NOW() WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("revoke owner: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *UserRepo) IsServerOwner(ctx context.Context, userID int64) (bool, error) {
	var owner bool
	err := r.pool.QueryRow(ctx, `SELECT is_server_owner FROM users WHERE id = $1`, userID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return owner, err
}

// IsServerOwnerByTelegramID is the DB-backed authorization check used by the
// legacy Telegram-ID-keyed routes (audit, groups) until they're rekeyed to
// user_id in Step 3. Returns false, not an error, when the Telegram ID has
// no identity yet.
func (r *UserRepo) IsServerOwnerByTelegramID(ctx context.Context, tgID int64) (bool, error) {
	var owner bool
	const q = `
		SELECT u.is_server_owner
		FROM user_identities ui
		JOIN users u ON u.id = ui.user_id
		WHERE ui.provider = $1 AND ui.external_id = $2`
	err := r.pool.QueryRow(ctx, q, models.IdentityProviderTelegram, strconv.FormatInt(tgID, 10)).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return owner, err
}

// TelegramID returns the Telegram external ID linked to userID.
// Returns pgx.ErrNoRows if the user has no telegram identity.
func (r *UserRepo) TelegramID(ctx context.Context, userID int64) (int64, error) {
	var externalID string
	const q = `SELECT external_id FROM user_identities WHERE user_id = $1 AND provider = $2`
	if err := r.pool.QueryRow(ctx, q, userID, models.IdentityProviderTelegram).Scan(&externalID); err != nil {
		return 0, err
	}
	return strconv.ParseInt(externalID, 10, 64)
}

func (r *UserRepo) SetDMLanguage(ctx context.Context, userID int64, language string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE users SET dm_language = $2, updated_at = NOW() WHERE id = $1`, userID, language)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *UserRepo) SetResultsOptOut(ctx context.Context, userID int64, optOut bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE users SET results_opt_out = $2, updated_at = NOW() WHERE id = $1`, userID, optOut)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	if err := row.Scan(&u.ID, &u.DisplayName, &u.IsServerOwner, &u.DMLanguage, &u.ResultsOptOut, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

func displayNameFor(username, firstName, lastName string) string {
	if username != "" {
		return "@" + username
	}
	return strings.TrimSpace(firstName + " " + lastName)
}

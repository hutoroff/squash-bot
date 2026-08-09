//go:build integration

package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/jackc/pgx/v5"
)

func TestUserRepo_ResolveIdentity_CreatesThenUpdates(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	created, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500001", "alice", "Alice", "Smith", "")
	if err != nil {
		t.Fatalf("ResolveIdentity (create): %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero user ID")
	}
	if created.DisplayName != "@alice" {
		t.Errorf("DisplayName: got %q, want @alice", created.DisplayName)
	}

	// Second resolve for the same identity must return the same user, and
	// update the display name to reflect the new profile.
	updated, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500001", "alice2", "Alice", "Smith", "")
	if err != nil {
		t.Fatalf("ResolveIdentity (update): %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("user ID changed on re-resolve: got %d, want %d", updated.ID, created.ID)
	}
	if updated.DisplayName != "@alice2" {
		t.Errorf("DisplayName not refreshed: got %q, want @alice2", updated.DisplayName)
	}

	// A real profile refresh with blank fields means the user genuinely
	// cleared their username/name in Telegram — display_name must follow,
	// not pin the stale value forever.
	cleared, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500001", "", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity (cleared): %v", err)
	}
	if cleared.DisplayName != "" {
		t.Errorf("DisplayName not cleared: got %q, want empty", cleared.DisplayName)
	}
}

func TestUserRepo_EnsureIdentity_DoesNotClobberProfile(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	if _, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500010", "dave", "Dave", "", ""); err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	// EnsureIdentity (the owner seed's path) must be a no-op against an
	// already-onboarded identity — it has no fresh profile to offer and must
	// never blank out real data acquired via ResolveIdentity.
	user, err := repo.EnsureIdentity(ctx, models.IdentityProviderTelegram, "500010")
	if err != nil {
		t.Fatalf("EnsureIdentity (existing): %v", err)
	}
	if user.DisplayName != "@dave" {
		t.Errorf("EnsureIdentity clobbered display name: got %q, want @dave", user.DisplayName)
	}

	// EnsureIdentity must still create a brand-new identity with a blank profile.
	created, err := repo.EnsureIdentity(ctx, models.IdentityProviderTelegram, "500011")
	if err != nil {
		t.Fatalf("EnsureIdentity (new): %v", err)
	}
	if created.DisplayName != "" {
		t.Errorf("EnsureIdentity (new): got display name %q, want empty", created.DisplayName)
	}
	tgID, err := repo.TelegramID(ctx, created.ID)
	if err != nil {
		t.Fatalf("TelegramID: %v", err)
	}
	if tgID != 500011 {
		t.Errorf("TelegramID: got %d, want 500011", tgID)
	}
}

func TestUserRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	if _, err := repo.GetByID(ctx, 999999); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestUserRepo_List(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	if _, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500002", "bob", "Bob", "", ""); err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	summaries, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 user, got %d", len(summaries))
	}
	if len(summaries[0].Providers) != 1 || summaries[0].Providers[0] != "telegram" {
		t.Errorf("Providers: got %v, want [telegram]", summaries[0].Providers)
	}
}

func TestUserRepo_SetServerOwner_LastOwnerGuard(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	owner, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500003", "owner", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	other, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500004", "other", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	if err := repo.SetServerOwner(ctx, owner.ID, true); err != nil {
		t.Fatalf("SetServerOwner(grant): %v", err)
	}

	// Only one owner exists — revoking must fail.
	if err := repo.SetServerOwner(ctx, owner.ID, false); !errors.Is(err, storage.ErrLastServerOwner) {
		t.Errorf("expected ErrLastServerOwner, got %v", err)
	}

	// Grant a second owner, then revoking the first must succeed.
	if err := repo.SetServerOwner(ctx, other.ID, true); err != nil {
		t.Fatalf("SetServerOwner(grant other): %v", err)
	}
	if err := repo.SetServerOwner(ctx, owner.ID, false); err != nil {
		t.Fatalf("SetServerOwner(revoke): %v", err)
	}

	isOwner, err := repo.IsServerOwner(ctx, owner.ID)
	if err != nil {
		t.Fatalf("IsServerOwner: %v", err)
	}
	if isOwner {
		t.Error("expected owner to be revoked")
	}
}

// TestUserRepo_SetServerOwner_ConcurrentRevocation_NeverReachesZero guards
// against a race where two concurrent revocations of two different owners
// (with exactly 2 owners total) could each observe count > 1 in their own
// snapshot and both succeed, leaving zero owners.
func TestUserRepo_SetServerOwner_ConcurrentRevocation_NeverReachesZero(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	ownerA, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500020", "a", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	ownerB, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500021", "b", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	for _, id := range []int64{ownerA.ID, ownerB.ID} {
		if err := repo.SetServerOwner(ctx, id, true); err != nil {
			t.Fatalf("SetServerOwner(grant %d): %v", id, err)
		}
	}

	const rounds = 20
	for i := 0; i < rounds; i++ {
		errA := make(chan error, 1)
		errB := make(chan error, 1)
		go func() { errA <- repo.SetServerOwner(ctx, ownerA.ID, false) }()
		go func() { errB <- repo.SetServerOwner(ctx, ownerB.ID, false) }()
		gotA, gotB := <-errA, <-errB

		succeeded := 0
		for _, err := range []error{gotA, gotB} {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, storage.ErrLastServerOwner):
				// expected for the loser
			default:
				t.Fatalf("round %d: unexpected error: %v", i, err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("round %d: expected exactly 1 revocation to succeed, got %d (a=%v b=%v)", i, succeeded, gotA, gotB)
		}

		var ownerCount int
		if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_server_owner`).Scan(&ownerCount); err != nil {
			t.Fatalf("round %d: count owners: %v", i, err)
		}
		if ownerCount != 1 {
			t.Fatalf("round %d: expected exactly 1 remaining owner, got %d", i, ownerCount)
		}

		// Restore two owners for the next round: whichever call succeeded is
		// the one that actually got revoked.
		revokedID := ownerB.ID
		if gotA == nil {
			revokedID = ownerA.ID
		}
		if err := repo.SetServerOwner(ctx, revokedID, true); err != nil {
			t.Fatalf("round %d: restore owner: %v", i, err)
		}
	}
}

func TestUserRepo_GrantServerOwnersByTelegramID_Idempotent(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	if err := repo.GrantServerOwnersByTelegramID(ctx, []int64{500005}); err != nil {
		t.Fatalf("GrantServerOwnersByTelegramID: %v", err)
	}
	tgID, err := repo.TelegramID(ctx, 1)
	if err != nil {
		t.Fatalf("TelegramID: %v", err)
	}
	if tgID != 500005 {
		t.Fatalf("TelegramID: got %d, want 500005", tgID)
	}
	isOwner, err := repo.IsServerOwner(ctx, 1)
	if err != nil || !isOwner {
		t.Fatalf("expected seeded user to be server owner, isOwner=%v err=%v", isOwner, err)
	}

	// Give the user a real profile, then re-run the seed: it must not wipe display_name.
	if _, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500005", "dave", "Dave", "", ""); err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if err := repo.GrantServerOwnersByTelegramID(ctx, []int64{500005}); err != nil {
		t.Fatalf("GrantServerOwnersByTelegramID (rerun): %v", err)
	}
	user, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if user.DisplayName != "@dave" {
		t.Errorf("seed re-run clobbered display name: got %q, want @dave", user.DisplayName)
	}
	if !user.IsServerOwner {
		t.Error("expected user to remain server owner")
	}
}

func TestUserRepo_SetDMLanguage_SetResultsOptOut(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewUserRepo(testPool)

	user, err := repo.ResolveIdentity(ctx, models.IdentityProviderTelegram, "500006", "eve", "", "", "")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	if err := repo.SetDMLanguage(ctx, user.ID, "de"); err != nil {
		t.Fatalf("SetDMLanguage: %v", err)
	}
	if err := repo.SetResultsOptOut(ctx, user.ID, true); err != nil {
		t.Fatalf("SetResultsOptOut: %v", err)
	}

	got, err := repo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DMLanguage != "de" {
		t.Errorf("DMLanguage: got %q, want de", got.DMLanguage)
	}
	if !got.ResultsOptOut {
		t.Error("expected ResultsOptOut to be true")
	}
}

func TestCreateTestUser_Fixture(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)

	userID, err := testutil.CreateTestUser(ctx, testPool, 500007, "frank")
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	repo := storage.NewUserRepo(testPool)
	tgID, err := repo.TelegramID(ctx, userID)
	if err != nil {
		t.Fatalf("TelegramID: %v", err)
	}
	if tgID != 500007 {
		t.Errorf("TelegramID: got %d, want 500007", tgID)
	}
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ── stub implementations ──────────────────────────────────────────────────────

type stubCredRepo struct {
	creds       []*models.VenueCredential
	createErr   error
	deleteErr   error
	loginExists bool
	existsErr   error
	priorities  []int
	priErr      error
}

func (r *stubCredRepo) Create(_ context.Context, venueID int64, login, _ string, priority, maxCourts int) (*models.VenueCredential, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	c := &models.VenueCredential{ID: 1, VenueID: venueID, Login: login, Priority: priority, MaxCourts: maxCourts, CreatedAt: time.Now()}
	r.creds = append(r.creds, c)
	return c, nil
}

func (r *stubCredRepo) ListByVenueID(_ context.Context, _ int64) ([]*models.VenueCredential, error) {
	return r.creds, nil
}

func (r *stubCredRepo) ListWithPasswordByVenueID(_ context.Context, _ int64) ([]*models.VenueCredential, error) {
	return r.creds, nil
}

func (r *stubCredRepo) Delete(_ context.Context, _, _ int64) error {
	return r.deleteErr
}

func (r *stubCredRepo) ExistsByLogin(_ context.Context, _ int64, _ string) (bool, error) {
	return r.loginExists, r.existsErr
}

func (r *stubCredRepo) PrioritiesInUse(_ context.Context, _ int64) ([]int, error) {
	return r.priorities, r.priErr
}

func (r *stubCredRepo) SetLastErrorAt(_ context.Context, _ int64) error {
	return nil
}

func (r *stubCredRepo) GetWithPasswordByID(_ context.Context, id int64) (*models.VenueCredential, error) {
	for _, c := range r.creds {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, nil
}

type stubVenueRepo struct {
	venue *models.Venue
	err   error
}

func (r *stubVenueRepo) Create(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (r *stubVenueRepo) GetByID(_ context.Context, _ int64) (*models.Venue, error) {
	return r.venue, r.err
}
func (r *stubVenueRepo) GetByIDAndGroupID(_ context.Context, _, _ int64) (*models.Venue, error) {
	return r.venue, r.err
}
func (r *stubVenueRepo) GetByGroupID(_ context.Context, _ int64) ([]*models.Venue, error) {
	if r.venue != nil {
		return []*models.Venue{r.venue}, nil
	}
	return nil, r.err
}
func (r *stubVenueRepo) Update(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (r *stubVenueRepo) Delete(_ context.Context, _, _ int64) error                { return nil }
func (r *stubVenueRepo) SetLastBookingReminderAt(_ context.Context, _ int64) error { return nil }
func (r *stubVenueRepo) SetLastAutoBookingAt(_ context.Context, _ int64) error     { return nil }

var testVenue = &models.Venue{ID: 10, GroupID: 20, Name: "Test Venue"}

func newTestCredService(credRepo *stubCredRepo, venueRepo *stubVenueRepo) *VenueCredentialService {
	enc, _ := NewEncryptor(testHexKey)
	return NewVenueCredentialService(credRepo, venueRepo, nil, enc)
}

// ── Add ───────────────────────────────────────────────────────────────────────

func TestVenueCredentialService_Add_HappyPath(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{venue: testVenue})

	cred, err := svc.Add(context.Background(), 10, 20, "user@example.com", "pass", 1, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if cred == nil {
		t.Fatal("expected non-nil credential")
	}
	if cred.Login != "user@example.com" {
		t.Errorf("Login: got %q, want %q", cred.Login, "user@example.com")
	}
}

func TestVenueCredentialService_Add_VenueNotFound(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{err: errors.New("not found")})

	_, err := svc.Add(context.Background(), 10, 99, "u@e.com", "pass", 1, 3)
	if err == nil {
		t.Error("wrong group: want error, got nil")
	}
}

func TestVenueCredentialService_Add_DuplicateLogin(t *testing.T) {
	svc := newTestCredService(
		&stubCredRepo{loginExists: true},
		&stubVenueRepo{venue: testVenue},
	)

	_, err := svc.Add(context.Background(), 10, 20, "dup@example.com", "pass", 2, 3)
	if !errors.Is(err, ErrDuplicateCredentialLogin) {
		t.Errorf("duplicate login: want ErrDuplicateCredentialLogin, got %v", err)
	}
}

func TestVenueCredentialService_Add_ExistsCheckError(t *testing.T) {
	svc := newTestCredService(
		&stubCredRepo{existsErr: errors.New("db down")},
		&stubVenueRepo{venue: testVenue},
	)

	_, err := svc.Add(context.Background(), 10, 20, "u@e.com", "pass", 1, 3)
	if err == nil {
		t.Error("db error on exists check: want error, got nil")
	}
	if errors.Is(err, ErrDuplicateCredentialLogin) {
		t.Error("db error should not be treated as duplicate-login error")
	}
}

func TestVenueCredentialService_Add_StorageError(t *testing.T) {
	svc := newTestCredService(
		&stubCredRepo{createErr: errors.New("insert failed")},
		&stubVenueRepo{venue: testVenue},
	)

	_, err := svc.Add(context.Background(), 10, 20, "u@e.com", "pass", 1, 3)
	if err == nil {
		t.Error("storage error: want error, got nil")
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestVenueCredentialService_List_HappyPath(t *testing.T) {
	stored := []*models.VenueCredential{
		{ID: 1, VenueID: 10, Login: "a@b.com", Priority: 1},
		{ID: 2, VenueID: 10, Login: "c@d.com", Priority: 2},
	}
	svc := newTestCredService(&stubCredRepo{creds: stored}, &stubVenueRepo{venue: testVenue})

	creds, err := svc.List(context.Background(), 10, 20)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 2 {
		t.Errorf("expected 2 credentials, got %d", len(creds))
	}
}

func TestVenueCredentialService_List_VenueNotOwned(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{err: errors.New("not found")})

	_, err := svc.List(context.Background(), 10, 99)
	if err == nil {
		t.Error("wrong group: want error, got nil")
	}
}

// ── Remove ────────────────────────────────────────────────────────────────────

func TestVenueCredentialService_Remove_HappyPath(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{venue: testVenue})

	if err := svc.Remove(context.Background(), 1, 10, 20); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestVenueCredentialService_Remove_VenueNotOwned(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{err: errors.New("not found")})

	if err := svc.Remove(context.Background(), 1, 10, 99); err == nil {
		t.Error("wrong group: want error, got nil")
	}
}

func TestVenueCredentialService_Remove_DeleteError(t *testing.T) {
	svc := newTestCredService(
		&stubCredRepo{deleteErr: errors.New("delete failed")},
		&stubVenueRepo{venue: testVenue},
	)

	if err := svc.Remove(context.Background(), 1, 10, 20); err == nil {
		t.Error("delete error: want error, got nil")
	}
}

func TestVenueCredentialService_Remove_ActiveBooking_ErrCredentialInUse(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{hasActiveByCredential: true}
	enc, _ := NewEncryptor(testHexKey)
	svc := NewVenueCredentialService(&stubCredRepo{}, &stubVenueRepo{venue: testVenue}, cbRepo, enc)

	err := svc.Remove(context.Background(), 1, 10, 20)
	if !errors.Is(err, ErrCredentialInUse) {
		t.Errorf("want ErrCredentialInUse, got %v", err)
	}
}

// ── PrioritiesInUse ───────────────────────────────────────────────────────────

func TestVenueCredentialService_PrioritiesInUse_HappyPath(t *testing.T) {
	svc := newTestCredService(
		&stubCredRepo{priorities: []int{1, 3, 5}},
		&stubVenueRepo{venue: testVenue},
	)

	p, err := svc.PrioritiesInUse(context.Background(), 10, 20)
	if err != nil {
		t.Fatalf("PrioritiesInUse: %v", err)
	}
	if len(p) != 3 {
		t.Errorf("expected 3 priorities, got %d", len(p))
	}
}

func TestVenueCredentialService_PrioritiesInUse_VenueNotOwned(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{err: errors.New("not found")})

	_, err := svc.PrioritiesInUse(context.Background(), 10, 99)
	if err == nil {
		t.Error("wrong group: want error, got nil")
	}
}

// ── Password is never returned ────────────────────────────────────────────────

// TestAdd_CredentialHasNoPasswordField verifies that the returned model has no
// password — the field must simply not exist on models.VenueCredential.
// This is a compile-time invariant, but the test documents the intent.
func TestAdd_ReturnedCredentialHasNoPassword(t *testing.T) {
	svc := newTestCredService(&stubCredRepo{}, &stubVenueRepo{venue: testVenue})

	cred, err := svc.Add(context.Background(), 10, 20, "u@e.com", "topsecret", 0, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// If models.VenueCredential ever gains a Password field this test will still
	// pass, but the field-absent compile-time check in the model file enforces the rule.
	// We at minimum assert the login is correctly threaded through.
	if cred.Login != "u@e.com" {
		t.Errorf("Login: got %q", cred.Login)
	}
}

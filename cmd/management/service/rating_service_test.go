package service

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// ── stub repos for rating tests ─────────────────────────────────────────────

type stubPlayerRatingRepo struct {
	ratings []*models.PlayerRating
}

func (r *stubPlayerRatingRepo) GetOrInit(_ context.Context, _, _ int64) (*models.PlayerRating, error) {
	return &models.PlayerRating{Rating: 1500, RD: 350, Volatility: 0.06}, nil
}

func (r *stubPlayerRatingRepo) Upsert(_ context.Context, _ *models.PlayerRating) error { return nil }

func (r *stubPlayerRatingRepo) ListByGroup(_ context.Context, _ int64) ([]*models.PlayerRating, error) {
	return r.ratings, nil
}

func (r *stubPlayerRatingRepo) ListGroupsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}

type stubRatingChangeRepo struct {
	changes []*models.RatingChange
}

func (r *stubRatingChangeRepo) Insert(_ context.Context, _ *models.RatingChange) error { return nil }

func (r *stubRatingChangeRepo) InsertInTx(_ context.Context, _ pgx.Tx, _ *models.RatingChange) error {
	return nil
}

func (r *stubRatingChangeRepo) ListByGroupAndDateRange(_ context.Context, _ int64, _, _ time.Time) ([]*models.RatingChange, error) {
	return r.changes, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

// ── tests ───────────────────────────────────────────────────────────────────

func TestGetLeaderboard_FiltersZeroGamePlayers(t *testing.T) {
	auditSvc, _ := newCaptureAuditSvc()

	ratingRepo := &stubPlayerRatingRepo{
		ratings: []*models.PlayerRating{
			{GroupID: 1, PlayerID: 10, Rating: 1600, RD: 200, GamesPlayed: 5, Player: &models.Player{ID: 10, FirstName: strPtr("Alice")}},
			{GroupID: 1, PlayerID: 20, Rating: 1500, RD: 350, GamesPlayed: 0, Player: &models.Player{ID: 20, FirstName: strPtr("Bob")}},
			{GroupID: 1, PlayerID: 30, Rating: 1550, RD: 250, GamesPlayed: 3, Player: &models.Player{ID: 30, FirstName: strPtr("Carol")}},
		},
	}

	svc := NewRatingService(
		nil, // pool not used by GetLeaderboard
		ratingRepo,
		&stubRatingChangeRepo{},
		&stubGroupRepoForDayAfter{},
		auditSvc,
		noopLogger(),
	)

	entries, err := svc.GetLeaderboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetLeaderboard returned error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (filtered out 0-game player), got %d", len(entries))
	}

	// Verify ranks are sequential after filtering.
	if entries[0].Rank != 1 {
		t.Errorf("first entry rank: got %d, want 1", entries[0].Rank)
	}
	if entries[1].Rank != 2 {
		t.Errorf("second entry rank: got %d, want 2", entries[1].Rank)
	}

	// Verify the correct players are present.
	if entries[0].Player.FirstName == nil || *entries[0].Player.FirstName != "Alice" {
		t.Errorf("first entry player: got %v, want %q", entries[0].Player.FirstName, "Alice")
	}
	if entries[1].Player.FirstName == nil || *entries[1].Player.FirstName != "Carol" {
		t.Errorf("second entry player: got %v, want %q", entries[1].Player.FirstName, "Carol")
	}
}

func TestGetLeaderboard_IncludesDeltaToday(t *testing.T) {
	auditSvc, _ := newCaptureAuditSvc()

	ratingRepo := &stubPlayerRatingRepo{
		ratings: []*models.PlayerRating{
			{GroupID: 1, PlayerID: 10, Rating: 1520, RD: 300, GamesPlayed: 2, Player: &models.Player{ID: 10, FirstName: strPtr("Dave")}},
		},
	}

	changeRepo := &stubRatingChangeRepo{
		changes: []*models.RatingChange{
			{GroupID: 1, PlayerID: 10, Delta: 10, AppliedAt: time.Now()},
			{GroupID: 1, PlayerID: 10, Delta: 5, AppliedAt: time.Now()},
		},
	}

	svc := NewRatingService(
		nil,
		ratingRepo,
		changeRepo,
		&stubGroupRepoForDayAfter{},
		auditSvc,
		noopLogger(),
	)

	entries, err := svc.GetLeaderboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetLeaderboard returned error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].DeltaToday != 15 {
		t.Errorf("DeltaToday: got %v, want 15", entries[0].DeltaToday)
	}
}

func TestGetLeaderboard_Empty(t *testing.T) {
	auditSvc, _ := newCaptureAuditSvc()

	svc := NewRatingService(
		nil,
		&stubPlayerRatingRepo{ratings: []*models.PlayerRating{}},
		&stubRatingChangeRepo{},
		&stubGroupRepoForDayAfter{},
		auditSvc,
		noopLogger(),
	)

	entries, err := svc.GetLeaderboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetLeaderboard returned error: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

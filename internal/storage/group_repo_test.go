//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

func TestGroupRepo_Upsert_Insert(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	if err := repo.Upsert(ctx, -100100, "Test Squad", true); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("GetAll: got %d groups, want 1", len(groups))
	}
	g := groups[0]
	if g.ChatID != -100100 {
		t.Errorf("ChatID: got %d, want -100100", g.ChatID)
	}
	if g.Title != "Test Squad" {
		t.Errorf("Title: got %q, want %q", g.Title, "Test Squad")
	}
	if !g.BotIsAdmin {
		t.Error("BotIsAdmin: got false, want true")
	}
}

func TestGroupRepo_Upsert_UpdateOnConflict(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	if err := repo.Upsert(ctx, -100200, "Old Title", true); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	// Second upsert: title changes, admin rights lost.
	if err := repo.Upsert(ctx, -100200, "New Title", false); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("GetAll: got %d groups, want 1 (conflict should update, not insert)", len(groups))
	}
	g := groups[0]
	if g.Title != "New Title" {
		t.Errorf("Title after update: got %q, want %q", g.Title, "New Title")
	}
	if g.BotIsAdmin {
		t.Error("BotIsAdmin after demotion upsert: got true, want false")
	}
}

// TestGroupRepo_Upsert_AdminFlagRoundTrips verifies every combination of the
// bot_is_admin flag so a re-upsert with a different status cannot silently corrupt it.
func TestGroupRepo_Upsert_AdminFlagRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo := storage.NewGroupRepo(testPool)

	cases := []struct {
		first, second bool
	}{
		{true, true},
		{false, false},
		{true, false},
		{false, true},
	}
	for _, tc := range cases {
		mustTruncate(t)
		if err := repo.Upsert(ctx, -100300, "G", tc.first); err != nil {
			t.Fatalf("first Upsert(%v): %v", tc.first, err)
		}
		if err := repo.Upsert(ctx, -100300, "G", tc.second); err != nil {
			t.Fatalf("second Upsert(%v): %v", tc.second, err)
		}
		groups, err := repo.GetAll(ctx)
		if err != nil {
			t.Fatalf("GetAll: %v", err)
		}
		if len(groups) != 1 {
			t.Fatalf("GetAll: got %d groups, want 1", len(groups))
		}
		if groups[0].BotIsAdmin != tc.second {
			t.Errorf("BotIsAdmin after %v->%v: got %v, want %v",
				tc.first, tc.second, groups[0].BotIsAdmin, tc.second)
		}
	}
}

func TestGroupRepo_Remove_ExistingGroup(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	if err := repo.Upsert(ctx, -100400, "Leave Me", false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Remove(ctx, -100400); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("GetAll after Remove: got %d groups, want 0", len(groups))
	}
}

func TestGroupRepo_Remove_NonExistentGroup(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	// Removing a chat_id that was never inserted must not return an error.
	if err := repo.Remove(ctx, -999999); err != nil {
		t.Errorf("Remove non-existent: got error %v, want nil", err)
	}
}

func TestGroupRepo_GetAll_Empty(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll on empty table: %v", err)
	}
	// Must return a non-error, zero-length result — not nil error with nil slice ambiguity.
	if groups == nil {
		// nil slice is acceptable; just ensure no error was returned.
	}
	if len(groups) != 0 {
		t.Errorf("GetAll empty: got %d groups, want 0", len(groups))
	}
}

func TestGroupRepo_GetAll_MultipleGroups(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	seed := []struct {
		chatID  int64
		title   string
		isAdmin bool
	}{
		{-100501, "Alpha", true},
		{-100502, "Beta", false},
		{-100503, "Gamma", true},
	}
	for _, s := range seed {
		if err := repo.Upsert(ctx, s.chatID, s.title, s.isAdmin); err != nil {
			t.Fatalf("Upsert %d: %v", s.chatID, err)
		}
	}

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != len(seed) {
		t.Fatalf("GetAll: got %d groups, want %d", len(groups), len(seed))
	}

	byID := make(map[int64]storage.BotGroup, len(groups))
	for _, g := range groups {
		byID[g.ChatID] = g
	}
	for _, s := range seed {
		g, ok := byID[s.chatID]
		if !ok {
			t.Errorf("missing group %d in GetAll result", s.chatID)
			continue
		}
		if g.Title != s.title {
			t.Errorf("group %d title: got %q, want %q", s.chatID, g.Title, s.title)
		}
		if g.BotIsAdmin != s.isAdmin {
			t.Errorf("group %d BotIsAdmin: got %v, want %v", s.chatID, g.BotIsAdmin, s.isAdmin)
		}
	}

	// Verify idempotency: a second Upsert for the same IDs must not duplicate rows.
	for _, s := range seed {
		if err := repo.Upsert(ctx, s.chatID, s.title, s.isAdmin); err != nil {
			t.Fatalf("re-Upsert %d: %v", s.chatID, err)
		}
	}
	groups2, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll after re-upsert: %v", err)
	}
	if len(groups2) != len(seed) {
		t.Errorf("GetAll after re-upsert: got %d groups (want %d), seeding is not idempotent",
			len(groups2), len(seed))
	}
}

func TestGroupRepo_Backfill_PreservesCorrectAdminStatus(t *testing.T) {
	// Verifies that a re-upsert for an existing group writes the provided status,
	// not silently ignoring it. An existing row with bot_is_admin=true must be
	// updatable to false (and vice-versa) via Upsert.
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	// Pre-existing row: bot is admin.
	if err := repo.Upsert(ctx, -100600, "Squash Club", true); err != nil {
		t.Fatalf("initial Upsert: %v", err)
	}

	// Re-upsert with admin=true must preserve the status.
	if err := repo.Upsert(ctx, -100600, "Squash Club", true); err != nil {
		t.Fatalf("re-upsert (admin=true): %v", err)
	}
	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != 1 || !groups[0].BotIsAdmin {
		t.Error("admin status incorrectly downgraded to false during re-seed")
	}

	// Re-upsert with admin=false must update the status.
	if err := repo.Upsert(ctx, -100600, "Squash Club", false); err != nil {
		t.Fatalf("re-upsert (admin=false): %v", err)
	}
	groups, err = repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(groups) != 1 || groups[0].BotIsAdmin {
		t.Error("admin status not updated to false after demotion re-seed")
	}
}

func TestGroupRepo_Truncate_ClearsBotGroups(t *testing.T) {
	// Regression: ensures Truncate() includes bot_groups so leaked rows from one
	// test cannot affect subsequent tests.
	ctx := context.Background()
	mustTruncate(t)
	repo := storage.NewGroupRepo(testPool)

	if err := repo.Upsert(ctx, -100700, "Temp", false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	groups, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll after Truncate: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("GetAll after Truncate: got %d groups, want 0 — bot_groups not cleared by Truncate", len(groups))
	}
}

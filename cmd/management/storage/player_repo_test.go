//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

func strPtr(s string) *string { return &s }

func TestPlayerRepo_Upsert_Create(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	userID, err := testutil.CreateTestUser(ctx, testPool, 100001, "alice")
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	repo := storage.NewPlayerRepo(testPool)

	got, err := repo.Upsert(ctx, userID)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if got.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if got.UserID != userID {
		t.Errorf("UserID: got %d, want %d", got.UserID, userID)
	}
	if got.TelegramID != 100001 {
		t.Errorf("TelegramID: got %d, want 100001", got.TelegramID)
	}
	if got.Username == nil || *got.Username != "alice" {
		t.Errorf("Username: got %v, want alice", got.Username)
	}
}

func TestPlayerRepo_Upsert_IsLazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	userID, err := testutil.CreateTestUser(ctx, testPool, 100002, "bob")
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	repo := storage.NewPlayerRepo(testPool)

	first, err := repo.Upsert(ctx, userID)
	if err != nil {
		t.Fatalf("Upsert (first): %v", err)
	}

	second, err := repo.Upsert(ctx, userID)
	if err != nil {
		t.Fatalf("Upsert (second): %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("ID changed on repeat upsert: got %d, want %d", second.ID, first.ID)
	}
}

func TestPlayerRepo_GetByUserID(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	userID, err := testutil.CreateTestUser(ctx, testPool, 100003, "charlie")
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	repo := storage.NewPlayerRepo(testPool)
	if _, err := repo.Upsert(ctx, userID); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.TelegramID != 100003 {
		t.Errorf("TelegramID: got %d, want 100003", got.TelegramID)
	}
}

func TestPlayerRepo_GetByUserID_NotFound(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewPlayerRepo(testPool)

	_, err := repo.GetByUserID(ctx, 999999)
	if err == nil {
		t.Error("expected error for unknown user_id, got nil")
	}
}

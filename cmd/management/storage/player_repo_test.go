//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

func strPtr(s string) *string { return &s }

func TestPlayerRepo_Upsert_Create(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewPlayerRepo(testPool)

	player := &models.Player{
		TelegramID: 100001,
		Username:   strPtr("alice"),
		FirstName:  strPtr("Alice"),
		LastName:   strPtr("Smith"),
	}
	got, err := repo.Upsert(ctx, player)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if got.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if got.TelegramID != 100001 {
		t.Errorf("TelegramID: got %d, want 100001", got.TelegramID)
	}
	if got.Username == nil || *got.Username != "alice" {
		t.Errorf("Username: got %v, want alice", got.Username)
	}
	if got.FirstName == nil || *got.FirstName != "Alice" {
		t.Errorf("FirstName: got %v, want Alice", got.FirstName)
	}
	if got.LastName == nil || *got.LastName != "Smith" {
		t.Errorf("LastName: got %v, want Smith", got.LastName)
	}
}

func TestPlayerRepo_Upsert_UpdatesExisting(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewPlayerRepo(testPool)

	// First insert
	original := &models.Player{TelegramID: 100002, Username: strPtr("bob"), FirstName: strPtr("Bob")}
	first, _ := repo.Upsert(ctx, original)

	// Update via upsert (same telegram_id, different username)
	updated := &models.Player{TelegramID: 100002, Username: strPtr("robert"), FirstName: strPtr("Robert"), LastName: strPtr("Jones")}
	second, err := repo.Upsert(ctx, updated)
	if err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("ID changed on upsert: got %d, want %d", second.ID, first.ID)
	}
	if second.Username == nil || *second.Username != "robert" {
		t.Errorf("Username not updated: got %v", second.Username)
	}
	if second.FirstName == nil || *second.FirstName != "Robert" {
		t.Errorf("FirstName not updated: got %v", second.FirstName)
	}
	if second.LastName == nil || *second.LastName != "Jones" {
		t.Errorf("LastName not updated: got %v", second.LastName)
	}
}

func TestPlayerRepo_GetByTelegramID(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewPlayerRepo(testPool)

	_, _ = repo.Upsert(ctx, &models.Player{TelegramID: 100003, Username: strPtr("charlie")})

	got, err := repo.GetByTelegramID(ctx, 100003)
	if err != nil {
		t.Fatalf("GetByTelegramID: %v", err)
	}
	if got.TelegramID != 100003 {
		t.Errorf("TelegramID: got %d, want 100003", got.TelegramID)
	}
}

func TestPlayerRepo_GetByTelegramID_NotFound(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewPlayerRepo(testPool)

	_, err := repo.GetByTelegramID(ctx, 999999)
	if err == nil {
		t.Error("expected error for unknown telegram_id, got nil")
	}
}

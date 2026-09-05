//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// apiTestPool is shared across all api integration tests in this package.
var apiTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	if err := testutil.CheckDocker(); err != nil {
		fmt.Fprintf(os.Stderr, "api integration tests require Docker: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		log.Fatalf("setup test db: %v", err)
	}
	apiTestPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// TestResolveIdentity_FindsExistingPlayer confirms resolve reports the
// player_id of a user who has already joined a game.
func TestResolveIdentity_FindsExistingPlayer(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, apiTestPool); err != nil {
		t.Fatal(err)
	}

	playerRepo := storage.NewPlayerRepo(apiTestPool)
	userRepo := storage.NewUserRepo(apiTestPool)

	const tgID = int64(600001)
	userID, err := testutil.CreateTestUser(ctx, apiTestPool, tgID, "alice")
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	player, err := playerRepo.Upsert(ctx, userID)
	if err != nil {
		t.Fatalf("Upsert player: %v", err)
	}

	h := &Handler{
		playerRepo: playerRepo,
		userRepo:   userRepo,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	body := fmt.Sprintf(`{"provider":"telegram","external_id":"%d","username":"alice","first_name":"Alice"}`, tgID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.resolveIdentity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("resolveIdentity: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp resolveIdentityResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PlayerID == nil {
		t.Fatal("expected player_id to be found, got nil")
	}
	if *resp.PlayerID != player.ID {
		t.Errorf("PlayerID: got %d, want %d", *resp.PlayerID, player.ID)
	}
	if resp.UserID != userID {
		t.Errorf("UserID: got %d, want %d", resp.UserID, userID)
	}
}

// TestResolveIdentity_NoPlayerYet confirms the ordinary case still reports
// player_id: null for a user who has never joined a game.
func TestResolveIdentity_NoPlayerYet(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, apiTestPool); err != nil {
		t.Fatal(err)
	}

	h := &Handler{
		playerRepo: storage.NewPlayerRepo(apiTestPool),
		userRepo:   storage.NewUserRepo(apiTestPool),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	body := `{"provider":"telegram","external_id":"600002","username":"bob"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.resolveIdentity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("resolveIdentity: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp resolveIdentityResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PlayerID != nil {
		t.Errorf("expected nil player_id, got %v", *resp.PlayerID)
	}
}

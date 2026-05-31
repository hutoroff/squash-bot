package service

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// ── mock GameResultRepository ────────────────────────────────────────────────

type mockGameResultRepo struct {
	pendingResults []*models.GameResult
	listErr        error
	decideCalls    []decideCall
	decideErr      error
}

type decideCall struct {
	ID     int64
	Status models.GameResultStatus
}

func (m *mockGameResultRepo) Create(_ context.Context, _ *models.GameResult) (int64, error) {
	return 0, nil
}

func (m *mockGameResultRepo) GetByID(_ context.Context, _ int64) (*models.GameResult, error) {
	return nil, nil
}

func (m *mockGameResultRepo) SetApprovalMessage(_ context.Context, _, _ int64, _ int) error {
	return nil
}

func (m *mockGameResultRepo) Decide(_ context.Context, id int64, status models.GameResultStatus, _ time.Time) error {
	m.decideCalls = append(m.decideCalls, decideCall{ID: id, Status: status})
	return m.decideErr
}

func (m *mockGameResultRepo) DecideInTx(_ context.Context, _ pgx.Tx, id int64, status models.GameResultStatus, _ time.Time) error {
	m.decideCalls = append(m.decideCalls, decideCall{ID: id, Status: status})
	return m.decideErr
}

func (m *mockGameResultRepo) ListPendingOlderThan(_ context.Context, _ time.Time) ([]*models.GameResult, error) {
	return m.pendingResults, m.listErr
}

func (m *mockGameResultRepo) ListByGroupAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}

func (m *mockGameResultRepo) ListByGameID(_ context.Context, _ int64) ([]*models.GameResult, error) {
	return nil, nil
}

// ── mock PlayerRepository ────────────────────────────────────────────────────

type mockPlayerRepo struct {
	players map[int64]*models.Player // keyed by player ID
}

func (m *mockPlayerRepo) Upsert(_ context.Context, p *models.Player) (*models.Player, error) {
	return p, nil
}

func (m *mockPlayerRepo) GetByTelegramID(_ context.Context, _ int64) (*models.Player, error) {
	return nil, nil
}

func (m *mockPlayerRepo) GetByID(_ context.Context, id int64) (*models.Player, error) {
	if m.players != nil {
		if p, ok := m.players[id]; ok {
			return p, nil
		}
	}
	return nil, nil
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestAutoApproveJob_ApprovesPendingOlderThan48h(t *testing.T) {
	resultRepo := &mockGameResultRepo{
		pendingResults: []*models.GameResult{
			{
				ID:          1,
				GroupID:     -1001,
				AuthorID:    10,
				OpponentID:  20,
				Status:      models.GameResultPending,
				SubmittedAt: time.Now().Add(-72 * time.Hour), // 72h ago — well past cutoff
			},
		},
	}
	auditSvc, auditRepo := newCaptureAuditSvc()
	api := &mockTelegramAPI{}
	playerRepo := &mockPlayerRepo{
		players: map[int64]*models.Player{
			10: {ID: 10, TelegramID: 100},
			20: {ID: 20, TelegramID: 200},
		},
	}

	job := NewAutoApproveResultsJob(api, nil, resultRepo, playerRepo, auditSvc, noopLogger())
	job.run(false)

	// Verify Decide was called with auto_approved.
	if len(resultRepo.decideCalls) != 1 {
		t.Fatalf("expected 1 Decide call, got %d", len(resultRepo.decideCalls))
	}
	dc := resultRepo.decideCalls[0]
	if dc.ID != 1 {
		t.Errorf("Decide result ID: got %d, want 1", dc.ID)
	}
	if dc.Status != models.GameResultAutoApproved {
		t.Errorf("Decide status: got %q, want %q", dc.Status, models.GameResultAutoApproved)
	}

	// Verify audit event was recorded.
	if len(auditRepo.inserted) == 0 {
		t.Error("expected at least one audit event, got none")
	}
}

func TestAutoApproveJob_SkipsRecentPending(t *testing.T) {
	// ListPendingOlderThan returns results older than the cutoff,
	// so a result submitted 1h ago would NOT be returned by the query.
	// We simulate this by returning an empty list.
	resultRepo := &mockGameResultRepo{
		pendingResults: nil, // query filters out recent results
	}
	auditSvc, _ := newCaptureAuditSvc()
	api := &mockTelegramAPI{}

	job := NewAutoApproveResultsJob(api, nil, resultRepo, &mockPlayerRepo{}, auditSvc, noopLogger())
	job.run(false)

	if len(resultRepo.decideCalls) != 0 {
		t.Errorf("expected 0 Decide calls for recent result, got %d", len(resultRepo.decideCalls))
	}
	if len(api.sendCalls) != 0 {
		t.Errorf("expected 0 Send calls, got %d", len(api.sendCalls))
	}
}

func TestAutoApproveJob_NoPendingResults(t *testing.T) {
	resultRepo := &mockGameResultRepo{
		pendingResults: []*models.GameResult{}, // explicitly empty
	}
	auditSvc, auditRepo := newCaptureAuditSvc()
	api := &mockTelegramAPI{}

	job := NewAutoApproveResultsJob(api, nil, resultRepo, &mockPlayerRepo{}, auditSvc, noopLogger())
	job.run(false)

	if len(resultRepo.decideCalls) != 0 {
		t.Errorf("expected 0 Decide calls, got %d", len(resultRepo.decideCalls))
	}
	if len(api.sendCalls) != 0 {
		t.Errorf("expected 0 Send calls, got %d", len(api.sendCalls))
	}
	if len(api.requestCalls) != 0 {
		t.Errorf("expected 0 Request calls, got %d", len(api.requestCalls))
	}
	if len(auditRepo.inserted) != 0 {
		t.Errorf("expected 0 audit events, got %d", len(auditRepo.inserted))
	}
}

func TestAutoApproveJob_EditsDMCard(t *testing.T) {
	approvalChatID := int64(200)
	approvalMsgID := 42

	resultRepo := &mockGameResultRepo{
		pendingResults: []*models.GameResult{
			{
				ID:                1,
				GroupID:           -1001,
				AuthorID:          10,
				OpponentID:        20,
				Status:            models.GameResultPending,
				SubmittedAt:       time.Now().Add(-72 * time.Hour),
				ApprovalChatID:    &approvalChatID,
				ApprovalMessageID: &approvalMsgID,
			},
		},
	}
	auditSvc, _ := newCaptureAuditSvc()
	api := &mockTelegramAPI{}
	playerRepo := &mockPlayerRepo{
		players: map[int64]*models.Player{
			10: {ID: 10, TelegramID: 100},
			20: {ID: 20, TelegramID: 200},
		},
	}

	job := NewAutoApproveResultsJob(api, nil, resultRepo, playerRepo, auditSvc, noopLogger())
	job.run(false)

	// Verify Request was called to edit the opponent DM card.
	if len(api.requestCalls) == 0 {
		t.Fatal("expected at least 1 Request call to edit DM card, got 0")
	}

	// The Request call should be an EditMessageTextConfig targeting the approval chat.
	edit, ok := api.requestCalls[0].(tgbotapi.EditMessageTextConfig)
	if !ok {
		t.Fatalf("expected EditMessageTextConfig, got %T", api.requestCalls[0])
	}
	if edit.ChatID != approvalChatID {
		t.Errorf("edit ChatID: got %d, want %d", edit.ChatID, approvalChatID)
	}
	if edit.MessageID != approvalMsgID {
		t.Errorf("edit MessageID: got %d, want %d", edit.MessageID, approvalMsgID)
	}
}

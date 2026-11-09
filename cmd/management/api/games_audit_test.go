package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

type recordingAuditRepo struct{ types []models.AuditEventType }

func (r *recordingAuditRepo) Insert(_ context.Context, evt *models.AuditEvent) error {
	r.types = append(r.types, evt.EventType)
	return nil
}
func (r *recordingAuditRepo) Query(context.Context, models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	return nil, nil
}
func (r *recordingAuditRepo) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func TestRecordCourtsUpdated_EmptyCourtsRecordsGameCanceled(t *testing.T) {
	for _, tc := range []struct {
		courts string
		want   models.AuditEventType
	}{
		{"1,2", models.AuditEventCourtsReserved},
		{"", models.AuditEventGameCanceled},
		{"  ", models.AuditEventGameCanceled},
	} {
		repo := &recordingAuditRepo{}
		h := &Handler{auditSvc: service.NewAuditService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))}

		h.recordCourtsUpdated(httptest.NewRequest("PATCH", "/", nil), 10, 20, 99, "Admin", tc.courts)

		if len(repo.types) != 1 || repo.types[0] != tc.want {
			t.Errorf("courts %q: recorded %v, want [%s]", tc.courts, repo.types, tc.want)
		}
	}
}

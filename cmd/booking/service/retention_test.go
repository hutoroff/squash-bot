package service

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

type fakeRetentionStore struct {
	cutoff time.Time
	limit  int
}

func (s *fakeRetentionStore) CompactEligibleResults(_ context.Context, cutoff time.Time, limit int) ([]bookingapi.OperationID, error) {
	s.cutoff, s.limit = cutoff, limit
	return []bookingapi.OperationID{operation1}, nil
}

func TestRetentionServiceUsesBoundedNinetyDayDefault(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	store := &fakeRetentionStore{}
	service, err := NewRetentionService(store, RetentionConfig{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || store.limit != DefaultCompactionBatch || !store.cutoff.Equal(now.Add(-DefaultResultRetention)) {
		t.Fatalf("compaction call = %+v cutoff=%v limit=%d", ids, store.cutoff, store.limit)
	}
}

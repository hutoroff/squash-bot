package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

const (
	DefaultResultRetention = 90 * 24 * time.Hour
	DefaultCompactionBatch = 100
)

type RetentionStore interface {
	CompactEligibleResults(context.Context, time.Time, int) ([]bookingapi.OperationID, error)
}

type RetentionConfig struct {
	ResultRetention time.Duration
	BatchSize       int
	Clock           func() time.Time
}

type RetentionService struct {
	store     RetentionStore
	retention time.Duration
	batchSize int
	clock     func() time.Time
}

func NewRetentionService(store RetentionStore, cfg RetentionConfig) (*RetentionService, error) {
	if store == nil {
		return nil, fmt.Errorf("booking retention store is required")
	}
	if cfg.ResultRetention == 0 {
		cfg.ResultRetention = DefaultResultRetention
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = DefaultCompactionBatch
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.ResultRetention <= 0 || cfg.BatchSize <= 0 || cfg.BatchSize > 1000 {
		return nil, fmt.Errorf("invalid booking retention configuration")
	}
	return &RetentionService{store: store, retention: cfg.ResultRetention, batchSize: cfg.BatchSize, clock: cfg.Clock}, nil
}

// RunOnce compacts one bounded batch. P6 owns scheduling; this method does not
// create a background worker or acquire execution authority.
func (s *RetentionService) RunOnce(ctx context.Context) ([]bookingapi.OperationID, error) {
	return s.store.CompactEligibleResults(ctx, s.clock().Add(-s.retention), s.batchSize)
}

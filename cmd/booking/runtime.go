package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/booking"
	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	bookingservice "github.com/hutoroff/squash-bot/cmd/booking/service"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	providerName    = "eversports"
	squashSportID   = "496"
	squashSportName = "Squash"
)

type runtimeHealthSource struct {
	runtime           atomic.Pointer[reliableRuntime]
	businessListening atomic.Bool
	draining          atomic.Bool
}

func (s *runtimeHealthSource) readiness(ctx context.Context) booking.Readiness {
	runtime := s.runtime.Load()
	if runtime == nil {
		return booking.Readiness{}
	}
	readiness := runtime.readiness(ctx)
	if !s.businessListening.Load() || s.draining.Load() {
		readiness.Ready = false
	}
	return readiness
}

func (s *runtimeHealthSource) running() int {
	runtime := s.runtime.Load()
	if runtime == nil || runtime.coordinator == nil {
		return 0
	}
	return runtime.coordinator.Running()
}

func (s *runtimeHealthSource) ledgerHealth(ctx context.Context) (storage.LedgerHealth, error) {
	runtime := s.runtime.Load()
	if runtime == nil || runtime.operations == nil {
		return storage.LedgerHealth{}, fmt.Errorf("booking runtime is initializing")
	}
	return runtime.operations.LedgerHealth(ctx)
}

type reliableRuntime struct {
	pool        *pgxpool.Pool
	guard       *storage.SingletonGuard
	coordinator *bookingservice.Coordinator
	retention   *bookingservice.RetentionService
	operations  *storage.OperationRepo
	scope       bookingapi.Scope
	logger      *slog.Logger
}

func buildReliableRuntime(ctx context.Context, cfg *config.BookingConfig, logger *slog.Logger, observer bookingservice.CoordinatorObserver) (*reliableRuntime, *booking.Handler, error) {
	return buildReliableRuntimeWithFactory(ctx, cfg, logger, observer, nil)
}

func buildReliableRuntimeWithFactory(ctx context.Context, cfg *config.BookingConfig, logger *slog.Logger, observer bookingservice.CoordinatorObserver, sessionFactory bookingservice.SessionFactory) (_ *reliableRuntime, handler *booking.Handler, err error) {
	identityKey, err := hex.DecodeString(cfg.IdentityKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decode booking identity key: %w", err)
	}
	if err := storage.RunMigrations(cfg.DatabaseURL); err != nil {
		return nil, nil, err
	}
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	runtime := &reliableRuntime{pool: pool, logger: logger}
	defer func() {
		if err != nil {
			runtime.closeStartupResources()
		}
	}()

	operations := storage.NewOperationRepo(pool)
	runtime.operations = operations
	evidence := storage.NewEvidenceRepo(pool)
	imports := storage.NewImportRepo(pool)
	if err := evidence.VerifyIdentityKey(ctx, identityKey); err != nil {
		return nil, nil, fmt.Errorf("verify booking identity key: %w", err)
	}
	guard, err := storage.AcquireSingletonGuard(ctx, cfg.DatabaseURL, cfg.SingletonCheckInterval)
	if err != nil {
		return nil, nil, err
	}
	runtime.guard = guard

	recovery, err := bookingservice.NewRecovery(operations, evidence, guard)
	if err != nil {
		return nil, nil, err
	}
	recovered, err := recovery.Run(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("recover booking operations: %w", err)
	}
	if len(recovered) > 0 {
		logger.Warn("recovered unfinished booking operations", "count", len(recovered))
	}

	scope := configuredScope(cfg)
	runtime.scope = scope
	factory := sessionFactory
	if factory == nil {
		factory = bookingservice.SessionFactoryFunc(func(_ context.Context, credentials bookingapi.Credentials) (bookingservice.Session, error) {
			return eversports.NewWithTimeout(credentials.Email, credentials.Password, logger, cfg.ProviderCallTimeout), nil
		})
	}
	sessions, err := bookingservice.NewSessionManager(factory, bookingservice.SessionManagerConfig{
		MaxEntries: cfg.SessionCapacity, IdleTTL: cfg.SessionIdleTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	runner, err := bookingservice.NewProviderRunner(operations, evidence, bookingservice.ProviderRunnerConfig{
		IdentityKey: identityKey, SportUUID: cfg.EversportsSquashUUID, PersistenceTimeout: cfg.FinalizationTimeout,
	})
	if err != nil {
		sessions.Close()
		return nil, nil, err
	}
	coordinator, err := bookingservice.NewCoordinator(operations, sessions, runner, guard, logger, bookingservice.CoordinatorConfig{
		MaxConcurrent: 1, AdmissionWait: cfg.AdmissionWait, ExecutionTimeout: cfg.ExecutionTimeout,
		FinalizationTimeout: cfg.FinalizationTimeout, Observer: observer,
	})
	if err != nil {
		sessions.Close()
		return nil, nil, err
	}
	runtime.coordinator = coordinator

	status, err := bookingservice.NewStatusService(operations, evidence)
	if err != nil {
		return nil, nil, err
	}
	reconciler, err := bookingservice.NewReconciliationService(operations, evidence, factory, status, bookingservice.ReconciliationConfig{
		Scope: scope, IdentityKey: identityKey, PersistenceTimeout: cfg.FinalizationTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	importer, err := bookingservice.NewImportService(imports, factory, bookingservice.ImportServiceConfig{
		Scope: scope, IdentityKey: identityKey, SportID: squashSportID, SportName: squashSportName,
		SportUUID: cfg.EversportsSquashUUID, PersistenceTimeout: cfg.FinalizationTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	retention, err := bookingservice.NewRetentionService(operations, bookingservice.RetentionConfig{
		ResultRetention: cfg.RetentionAge, BatchSize: cfg.RetentionBatchSize,
	})
	if err != nil {
		return nil, nil, err
	}
	runtime.retention = retention

	handler = booking.NewHandler(logger, Version, cfg.EversportsFacilityID, cfg.EversportsFacilityUUID, cfg.EversportsFacilitySlug)
	if err := handler.ConfigureReliableAPI(booking.ReliableAPIConfig{
		Coordinator: coordinator, Status: status, Reconciler: reconciler, Importer: importer,
		Scope: scope, IdentityKey: identityKey, ResponseWait: cfg.HTTPResponseWait,
		ProviderCallTimeout: cfg.ProviderCallTimeout, ProviderSportUUID: cfg.EversportsSquashUUID,
	}); err != nil {
		return nil, nil, err
	}
	return runtime, handler, nil
}

func configuredScope(cfg *config.BookingConfig) bookingapi.Scope {
	return bookingapi.Scope{
		ServiceKey: cfg.ServiceKey, Provider: providerName,
		FacilityID: cfg.EversportsFacilityID, FacilityUUID: cfg.EversportsFacilityUUID,
		FacilitySlug: cfg.EversportsFacilitySlug, Sport: "squash",
	}
}

func (r *reliableRuntime) readiness(ctx context.Context) booking.Readiness {
	readiness := booking.Readiness{
		Singleton:   r.guard != nil && r.guard.Healthy(),
		Coordinator: r.coordinator != nil && r.coordinator.Ready(),
	}
	readiness.Database = r.pool != nil && r.pool.Ping(ctx) == nil
	readiness.Ready = readiness.Database && readiness.Singleton && readiness.Coordinator
	return readiness
}

func (r *reliableRuntime) runRetention(ctx context.Context, interval time.Duration) {
	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		compacted, err := r.retention.RunOnce(runCtx)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.Error("compact booking results", "err", err)
			}
			return
		}
		if len(compacted) > 0 {
			r.logger.Info("compacted booking results", "count", len(compacted))
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (r *reliableRuntime) shutdown(ctx context.Context) error {
	if r.coordinator != nil {
		r.coordinator.CloseAdmission()
		if err := r.coordinator.Shutdown(ctx); err != nil {
			return fmt.Errorf("drain booking coordinator: %w", err)
		}
	}
	var guardErr error
	if r.guard != nil {
		if err := r.guard.Close(ctx); err != nil {
			guardErr = err
		}
		r.guard = nil
	}
	if r.pool != nil {
		r.pool.Close()
		r.pool = nil
	}
	return guardErr
}

func (r *reliableRuntime) closeStartupResources() {
	if r.coordinator != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = r.coordinator.Shutdown(ctx)
		cancel()
	}
	if r.guard != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = r.guard.Close(ctx)
		cancel()
	}
	if r.pool != nil {
		r.pool.Close()
	}
}

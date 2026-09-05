//go:build integration

package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/featureflags"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

func TestFeatureFlags_PersistenceOverridesAndAudit(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	ownerID := mustCreateUser(t, ctx, 701, "owner")
	otherID := mustCreateUser(t, ctx, 702, "not_owner")
	users := storage.NewUserRepo(testPool)
	if err := users.SetServerOwner(ctx, ownerID, true); err != nil {
		t.Fatal(err)
	}
	group := int64(-701)
	groups := storage.NewGroupRepo(testPool)
	if err := groups.Upsert(ctx, group, "Flag test", true); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	audit := service.NewAuditService(storage.NewAuditEventRepo(testPool), logger)
	flags := storage.NewFeatureFlagRepo(testPool)
	flagSvc := service.NewFeatureFlagService(flags, users, groups, audit)
	yes, no := true, false
	s, err := flags.Get(ctx, featureflags.ScoreAwareRating, &group)
	if err != nil || s.Enabled || s.Global != nil || s.Override != nil {
		t.Fatalf("default %+v %v", s, err)
	}
	for _, step := range []struct {
		scope   *int64
		value   *bool
		enabled bool
		source  string
	}{
		{nil, &yes, true, "global"}, {&group, &no, false, "group"}, {&group, nil, true, "global"},
		{nil, &no, false, "global"}, {&group, &yes, true, "group"}, {&group, nil, false, "global"}, {nil, nil, false, "default"},
	} {
		if err := flagSvc.Set(ctx, ownerID, "owner", featureflags.ScoreAwareRating, step.scope, step.value); err != nil {
			t.Fatal(err)
		}
		// A fresh repository has no process-local cached value.
		got, err := storage.NewFeatureFlagRepo(testPool).Get(ctx, featureflags.ScoreAwareRating, &group)
		if err != nil || got.Enabled != step.enabled || got.Source != step.source {
			t.Fatalf("state %+v %v", got, err)
		}
	}
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE event_type='feature_flag.changed' AND actor_user_id=$1 AND metadata ? 'old_override' AND metadata ? 'new_override'`, ownerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("audit events %d", count)
	}
	if err := flagSvc.Set(ctx, otherID, "not owner", featureflags.ScoreAwareRating, &group, &yes); !errors.Is(err, service.ErrFeatureFlagForbidden) {
		t.Fatalf("non-owner mutation: %v", err)
	}
	missing := int64(-999)
	if err := flagSvc.Set(ctx, ownerID, "owner", featureflags.ScoreAwareRating, &missing, &yes); !errors.Is(err, service.ErrFeatureFlagScope) {
		t.Fatalf("unknown group: %v", err)
	}
}

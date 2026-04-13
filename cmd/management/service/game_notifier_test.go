package service

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func TestResolveGroupTimezone(t *testing.T) {
	utc := time.UTC
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}

	cases := []struct {
		name       string
		group      *models.Group
		defaultLoc *time.Location
		wantLoc    *time.Location
	}{
		{
			name:       "empty timezone falls back to default",
			group:      &models.Group{Timezone: ""},
			defaultLoc: utc,
			wantLoc:    utc,
		},
		{
			name:       "valid IANA timezone is used",
			group:      &models.Group{Timezone: "Europe/Berlin"},
			defaultLoc: utc,
			wantLoc:    berlin,
		},
		{
			name:       "invalid IANA timezone falls back to default",
			group:      &models.Group{Timezone: "Not/A/Timezone"},
			defaultLoc: utc,
			wantLoc:    utc,
		},
		{
			name:       "invalid timezone falls back to non-UTC default",
			group:      &models.Group{Timezone: "garbage"},
			defaultLoc: berlin,
			wantLoc:    berlin,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveGroupTimezone(tc.group, tc.defaultLoc, testLogger)
			if got.String() != tc.wantLoc.String() {
				t.Errorf("resolveGroupTimezone: want %q, got %q", tc.wantLoc, got)
			}
		})
	}
}

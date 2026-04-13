package gameformat_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

func strPtr(s string) *string { return &s }

// ── PlayerDisplayName ─────────────────────────────────────────────────────────

func TestPlayerDisplayName(t *testing.T) {
	cases := []struct {
		name   string
		player models.Player
		want   string
	}{
		{
			name:   "username preferred over full name",
			player: models.Player{Username: strPtr("alice"), FirstName: strPtr("Alice"), LastName: strPtr("Smith")},
			want:   "@alice",
		},
		{
			name:   "full name when username is nil",
			player: models.Player{FirstName: strPtr("Alice"), LastName: strPtr("Smith")},
			want:   "Alice Smith",
		},
		{
			name:   "first name only",
			player: models.Player{FirstName: strPtr("Alice")},
			want:   "Alice",
		},
		{
			name:   "last name only",
			player: models.Player{LastName: strPtr("Smith")},
			want:   "Smith",
		},
		{
			name:   "empty username falls back to name",
			player: models.Player{Username: strPtr(""), FirstName: strPtr("Alice"), LastName: strPtr("Smith")},
			want:   "Alice Smith",
		},
		{
			name:   "no fields at all",
			player: models.Player{},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gameformat.PlayerDisplayName(&tc.player)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// ── GameKeyboard ──────────────────────────────────────────────────────────────

func TestGameKeyboard_Structure(t *testing.T) {
	const gameID int64 = 42
	lz := i18n.New(i18n.En)
	kb := gameformat.GameKeyboard(gameID, lz)

	if len(kb.InlineKeyboard) != 2 {
		t.Fatalf("want 2 rows, got %d", len(kb.InlineKeyboard))
	}
	for i, row := range kb.InlineKeyboard {
		if len(row) != 2 {
			t.Errorf("row %d: want 2 buttons, got %d", i, len(row))
		}
	}
}

func TestGameKeyboard_CallbackData(t *testing.T) {
	const gameID int64 = 7
	lz := i18n.New(i18n.En)
	kb := gameformat.GameKeyboard(gameID, lz)

	want := []string{
		fmt.Sprintf("join:%d", gameID),
		fmt.Sprintf("skip:%d", gameID),
		fmt.Sprintf("guest_add:%d", gameID),
		fmt.Sprintf("guest_remove:%d", gameID),
	}
	var got []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == nil {
				t.Fatal("button has nil CallbackData")
			}
			got = append(got, *btn.CallbackData)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("want %d callback values, got %d", len(want), len(got))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("button %d: want callback %q, got %q", i, w, got[i])
		}
	}
}

func TestGameKeyboard_ButtonsHaveText(t *testing.T) {
	lz := i18n.New(i18n.En)
	kb := gameformat.GameKeyboard(1, lz)
	for i, row := range kb.InlineKeyboard {
		for j, btn := range row {
			if strings.TrimSpace(btn.Text) == "" {
				t.Errorf("row %d, button %d: text is empty", i, j)
			}
		}
	}
}

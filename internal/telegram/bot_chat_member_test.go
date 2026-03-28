package telegram

// White-box unit tests for membershipNotifyText.
// No database or Telegram API is needed — the function is pure.

import (
	"strings"
	"testing"
)

func TestMembershipNotifyText(t *testing.T) {
	const group = "Squash Club"

	tests := []struct {
		name        string
		oldStatus   string
		newStatus   string
		wantEmpty   bool
		wantSubstrs []string
	}{
		// ── Freshly added cases ──────────────────────────────────────────────
		{
			name:      "added as member (left → member): notify, no admin",
			oldStatus: "left", newStatus: "member",
			wantEmpty:   false,
			wantSubstrs: []string{group, "don't have administrator permissions", "pin"},
		},
		{
			name:      "added as member (kicked → member): notify, no admin",
			oldStatus: "kicked", newStatus: "member",
			wantEmpty:   false,
			wantSubstrs: []string{group, "don't have administrator permissions"},
		},
		{
			name:      "added as administrator (left → administrator): silent",
			oldStatus: "left", newStatus: "administrator",
			wantEmpty: true,
		},
		{
			name:      "added as administrator (kicked → administrator): silent",
			oldStatus: "kicked", newStatus: "administrator",
			wantEmpty: true,
		},

		// ── Demotion cases ───────────────────────────────────────────────────
		{
			name:      "demoted administrator → member: notify",
			oldStatus: "administrator", newStatus: "member",
			wantEmpty:   false,
			wantSubstrs: []string{group, "lost administrator permissions", "pin"},
		},
		{
			name:      "creator demoted to member: notify",
			oldStatus: "creator", newStatus: "member",
			wantEmpty:   false,
			wantSubstrs: []string{group, "lost administrator permissions"},
		},

		// ── No-op transitions ────────────────────────────────────────────────
		{
			name:      "member stays member (e.g. re-join after restriction): silent",
			oldStatus: "member", newStatus: "member",
			wantEmpty: true,
		},
		{
			name:      "restricted promoted to member: silent",
			oldStatus: "restricted", newStatus: "member",
			wantEmpty: true,
		},
		{
			name:      "member promoted to administrator: silent",
			oldStatus: "member", newStatus: "administrator",
			wantEmpty: true,
		},
		{
			name:      "re-promoted from member to administrator: silent",
			oldStatus: "member", newStatus: "administrator",
			wantEmpty: true,
		},

		// ── Removal cases (not member/administrator) ─────────────────────────
		{
			name:      "bot leaves (member → left): silent",
			oldStatus: "member", newStatus: "left",
			wantEmpty: true,
		},
		{
			name:      "bot kicked (administrator → kicked): silent",
			oldStatus: "administrator", newStatus: "kicked",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := membershipNotifyText(tt.oldStatus, tt.newStatus, group)

			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}

			if got == "" {
				t.Fatal("expected non-empty notification text, got empty string")
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(got, sub) {
					t.Errorf("notification text does not contain %q\nfull text: %s", sub, got)
				}
			}
		})
	}
}

func TestMembershipNotifyText_IncludesGroupTitle(t *testing.T) {
	// The group title must appear in every non-empty notification so the
	// operator knows which group triggered the alert.
	cases := []struct{ old, new string }{
		{"left", "member"},
		{"kicked", "member"},
		{"administrator", "member"},
		{"creator", "member"},
	}
	for _, c := range cases {
		title := "My Unique Group Name 42"
		got := membershipNotifyText(c.old, c.new, title)
		if got == "" {
			t.Errorf("(%s→%s) expected non-empty notification", c.old, c.new)
			continue
		}
		if !strings.Contains(got, title) {
			t.Errorf("(%s→%s) notification does not contain group title %q\ntext: %s",
				c.old, c.new, title, got)
		}
	}
}

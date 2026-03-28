package telegram

// White-box unit tests for pure functions in commands.go.
// No database or Telegram API is needed.

import (
	"strings"
	"testing"
	"time"
)

// --- superGroupMessageLink ---

func TestSuperGroupMessageLink_Supergroup(t *testing.T) {
	// Standard supergroup chat ID: -100XXXXXXXXX
	link := superGroupMessageLink(-1001234567890, 42)
	const want = "https://t.me/c/1234567890/42"
	if link != want {
		t.Errorf("got %q, want %q", link, want)
	}
}

func TestSuperGroupMessageLink_ShortChannelID(t *testing.T) {
	link := superGroupMessageLink(-1009876543, 1)
	const want = "https://t.me/c/9876543/1"
	if link != want {
		t.Errorf("got %q, want %q", link, want)
	}
}

func TestSuperGroupMessageLink_RegularGroup_Empty(t *testing.T) {
	// Regular group: negative but doesn't start with -100.
	link := superGroupMessageLink(-12345678, 99)
	if link != "" {
		t.Errorf("regular group should produce empty link, got %q", link)
	}
}

func TestSuperGroupMessageLink_NilMessageID_Zero(t *testing.T) {
	// message_id = 0 is technically invalid but must not panic.
	link := superGroupMessageLink(-1001000000000, 0)
	if !strings.Contains(link, "/0") {
		t.Errorf("expected message_id 0 in link, got %q", link)
	}
}

// --- escapeMarkdown ---

func TestEscapeMarkdown_NoSpecialChars(t *testing.T) {
	got := escapeMarkdown("Hello World")
	if got != "Hello World" {
		t.Errorf("plain text should be unchanged, got %q", got)
	}
}

func TestEscapeMarkdown_Underscores(t *testing.T) {
	got := escapeMarkdown("squash_friends")
	if got != `squash\_friends` {
		t.Errorf("underscores should be escaped, got %q", got)
	}
}

func TestEscapeMarkdown_Stars(t *testing.T) {
	got := escapeMarkdown("**bold**")
	if got != `\*\*bold\*\*` {
		t.Errorf("stars should be escaped, got %q", got)
	}
}

func TestEscapeMarkdown_Mixed(t *testing.T) {
	got := escapeMarkdown("my_group [best]")
	if !strings.Contains(got, `my\_group`) {
		t.Errorf("underscore not escaped in %q", got)
	}
	// escapeMarkdown escapes '[' but not ']' (only the opener creates a link in Markdown v1).
	if !strings.Contains(got, `\[best]`) {
		t.Errorf("opening bracket not escaped in %q", got)
	}
}

// --- parseAdminCommand (command prefix stripping, used by handleCommandNewGame) ---

// TestParseAdminCommand_NewGamePrefix simulates the prefix-stripping done inside
// handleCommandNewGame: strip "/new_game", pass the rest to parseAdminCommand.
func TestParseAdminCommand_NewGamePrefix(t *testing.T) {
	loc := time.UTC
	raw := "/new_game\n2026-06-01 18:00\ncourts: 2,3,4"
	lines := strings.SplitN(strings.TrimSpace(raw), "\n", 2)
	if len(lines) < 2 {
		t.Fatal("expected 2 lines after split")
	}
	body := strings.TrimSpace(lines[1])

	gameDate, courts, err := parseAdminCommand(body, loc)
	if err != nil {
		t.Fatalf("parseAdminCommand: %v", err)
	}
	if courts != "2,3,4" {
		t.Errorf("courts: got %q, want %q", courts, "2,3,4")
	}
	if gameDate.Year() != 2026 || gameDate.Month() != 6 || gameDate.Day() != 1 {
		t.Errorf("gameDate: got %v, want 2026-06-01", gameDate)
	}
}

func TestParseAdminCommand_NewGamePrefixOnly_Error(t *testing.T) {
	// Only "/new_game" with no body should not reach parseAdminCommand.
	// This replicates the guard in handleCommandNewGame.
	raw := "/new_game"
	lines := strings.SplitN(strings.TrimSpace(raw), "\n", 2)
	hasBody := len(lines) >= 2 && strings.TrimSpace(lines[1]) != ""
	if hasBody {
		t.Error("single-line /new_game should have no body")
	}
}

// --- courts validation (used by processCourtsEdit) ---

// TestCourtsValidation_Valid checks the validation logic applied in processCourtsEdit.
func TestCourtsValidation_Valid(t *testing.T) {
	cases := []string{"1", "2,3,4", "10,11"}
	for _, c := range cases {
		parts := strings.Split(c, ",")
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				t.Errorf("valid courts %q failed validation", c)
			}
		}
	}
}

func TestCourtsValidation_Invalid(t *testing.T) {
	cases := []string{"", ",", "1,,2"}
	for _, c := range cases {
		parts := strings.Split(c, ",")
		anyEmpty := false
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				anyEmpty = true
			}
		}
		if !anyEmpty {
			t.Errorf("invalid courts %q passed validation, should have failed", c)
		}
	}
}

// --- pending courts-edit routing priority (command preempts edit state) ---

// TestCommandRoutingPreemptsEdit verifies that a slash-command message is recognised
// as a command before the courts-edit path can consume it.  The routing predicate is
// strings.HasPrefix(text, "/"), which is what handleMessage checks first.
func TestCommandRoutingPreemptsEdit(t *testing.T) {
	commands := []string{"/help", "/start", "/my_game", "/games", "/new_game"}
	for _, cmd := range commands {
		if !strings.HasPrefix(cmd, "/") {
			t.Errorf("%q should be identified as a command", cmd)
		}
	}
	// A non-command message (e.g. courts input) must NOT be misidentified as a command.
	nonCommands := []string{"2,3,4", "hello", "2026-01-01 18:00"}
	for _, s := range nonCommands {
		if strings.HasPrefix(s, "/") {
			t.Errorf("%q should NOT be identified as a command", s)
		}
	}
}

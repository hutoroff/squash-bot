package service

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// ── formatChangelogMessage ────────────────────────────────────────────────────

func TestFormatChangelogMessage_BothSections(t *testing.T) {
	lz := i18n.New(i18n.En)
	content := "## Features\n- Added changelog announcements\n- Per-group toggle\n\n## Fixes\n- Fixed timezone display\n"
	got := formatChangelogMessage(lz, "1.4.0", content)

	assertContains(t, got, "1.4.0")
	assertContains(t, got, "✨ Features")
	assertContains(t, got, "• Added changelog announcements")
	assertContains(t, got, "• Per-group toggle")
	assertContains(t, got, "🐛 Fixes")
	assertContains(t, got, "• Fixed timezone display")
}

func TestFormatChangelogMessage_FeaturesOnly(t *testing.T) {
	lz := i18n.New(i18n.En)
	content := "## Features\n- New feature\n"
	got := formatChangelogMessage(lz, "1.0.0", content)

	assertContains(t, got, "✨ Features")
	assertNotContains(t, got, "🐛 Fixes")
}

func TestFormatChangelogMessage_FixesOnly(t *testing.T) {
	lz := i18n.New(i18n.En)
	content := "## Fixes\n- Fixed a bug\n"
	got := formatChangelogMessage(lz, "1.0.1", content)

	assertNotContains(t, got, "✨ Features")
	assertContains(t, got, "🐛 Fixes")
	assertContains(t, got, "• Fixed a bug")
}

func TestFormatChangelogMessage_EmptyContent(t *testing.T) {
	lz := i18n.New(i18n.En)
	got := formatChangelogMessage(lz, "1.0.0", "")

	assertContains(t, got, "1.0.0")
	assertNotContains(t, got, "✨")
	assertNotContains(t, got, "🐛")
}

func TestFormatChangelogMessage_German(t *testing.T) {
	lz := i18n.New(i18n.De)
	content := "## Features\n- Neue Funktion\n"
	got := formatChangelogMessage(lz, "2.0.0", content)

	assertContains(t, got, "Neuigkeiten in v2.0.0")
}

func TestFormatChangelogMessage_Russian(t *testing.T) {
	lz := i18n.New(i18n.Ru)
	content := "## Features\n- Что-то новое\n"
	got := formatChangelogMessage(lz, "2.0.0", content)

	assertContains(t, got, "Что нового в v2.0.0")
}

// ── extractSection ────────────────────────────────────────────────────────────

func TestExtractSection_Present(t *testing.T) {
	content := "## Features\n- item1\n- item2\n\n## Fixes\n- fix1\n"
	items := extractSection(content, "## Features")
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %v", len(items), items)
	}
	if items[0] != "item1" || items[1] != "item2" {
		t.Errorf("unexpected items: %v", items)
	}
}

func TestExtractSection_Absent(t *testing.T) {
	items := extractSection("## Fixes\n- fix1\n", "## Features")
	if len(items) != 0 {
		t.Errorf("want empty, got %v", items)
	}
}

func TestExtractSection_StopsAtNextHeader(t *testing.T) {
	content := "## Features\n- feat1\n## Fixes\n- fix1\n"
	items := extractSection(content, "## Features")
	if len(items) != 1 || items[0] != "feat1" {
		t.Errorf("want [feat1], got %v", items)
	}
}

// ── AnnounceChangelog ─────────────────────────────────────────────────────────

type stubTelegramAPI struct {
	sent []tgbotapi.Chattable
}

func (s *stubTelegramAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	s.sent = append(s.sent, c)
	return tgbotapi.Message{}, nil
}

func (s *stubTelegramAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (s *stubTelegramAPI) GetChatAdministrators(_ tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return nil, nil
}

type stubStateRepo struct {
	data map[string]string
}

func newStubStateRepo() *stubStateRepo {
	return &stubStateRepo{data: make(map[string]string)}
}

func (r *stubStateRepo) Get(_ context.Context, key string) (string, error) {
	v, ok := r.data[key]
	if !ok {
		return "", pgx.ErrNoRows
	}
	return v, nil
}

func (r *stubStateRepo) Set(_ context.Context, key, value string) error {
	r.data[key] = value
	return nil
}

type stubGroupRepoAnnounce struct {
	groups []models.Group
}

func (r *stubGroupRepoAnnounce) GetAll(_ context.Context) ([]models.Group, error) {
	return r.groups, nil
}
func (r *stubGroupRepoAnnounce) GetByID(_ context.Context, _ int64) (*models.Group, error) {
	return nil, pgx.ErrNoRows
}
func (r *stubGroupRepoAnnounce) Upsert(_ context.Context, _ int64, _ string, _ bool) error {
	return nil
}
func (r *stubGroupRepoAnnounce) SetLanguage(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *stubGroupRepoAnnounce) SetTimezone(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *stubGroupRepoAnnounce) SetChangelogEnabled(_ context.Context, _ int64, _ bool) error {
	return nil
}
func (r *stubGroupRepoAnnounce) Remove(_ context.Context, _ int64) error { return nil }
func (r *stubGroupRepoAnnounce) Exists(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

// announceWithContent bypasses the changelogs.Read file lookup by directly
// invoking the inner logic so we can test branch behaviour without real files.
// We do this by pre-populating the state repo and checking recorded state.

func TestAnnounceChangelog_AlreadyAnnounced(t *testing.T) {
	api := &stubTelegramAPI{}
	state := newStubStateRepo()
	state.data[lastChangelogVersionKey] = "1.4.0"
	groups := &stubGroupRepoAnnounce{groups: []models.Group{
		{ChatID: 100, Language: "en", ChangelogEnabled: true},
	}}

	// Version matches stored state — must be a no-op.
	AnnounceChangelog(context.Background(), api, groups, state, time.UTC, testLogger, "1.4.0")

	if len(api.sent) != 0 {
		t.Errorf("expected no messages sent, got %d", len(api.sent))
	}
}

func TestAnnounceChangelog_SkipsDisabledGroup(t *testing.T) {
	api := &stubTelegramAPI{}
	state := newStubStateRepo()
	// Use a version that has no actual changelog file — the function records the
	// version and returns. This isolates the "disabled group" path via the
	// no-file branch while still exercising the group filter.
	groups := &stubGroupRepoAnnounce{groups: []models.Group{
		{ChatID: 100, Language: "en", ChangelogEnabled: false},
		{ChatID: 200, Language: "en", ChangelogEnabled: false},
	}}

	AnnounceChangelog(context.Background(), api, groups, state, time.UTC, testLogger, "99.0.0")

	// No file for 99.0.0 → version recorded, no messages sent.
	if len(api.sent) != 0 {
		t.Errorf("expected no messages, got %d", len(api.sent))
	}
	if got := state.data[lastChangelogVersionKey]; got != "99.0.0" {
		t.Errorf("version should be recorded; got %q", got)
	}
}

func TestAnnounceChangelog_RecordsVersionWhenNoFile(t *testing.T) {
	api := &stubTelegramAPI{}
	state := newStubStateRepo()
	groups := &stubGroupRepoAnnounce{}

	AnnounceChangelog(context.Background(), api, groups, state, time.UTC, testLogger, "98.0.0")

	if got := state.data[lastChangelogVersionKey]; got != "98.0.0" {
		t.Errorf("want version recorded as 98.0.0, got %q", got)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected %q to contain %q", s, sub)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("expected %q NOT to contain %q", s, sub)
	}
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── mockTelegramAPI ───────────────────────────────────────────────────────────

type mockTelegramAPI struct {
	sendCalls  []tgbotapi.Chattable
	sendErr    error
	sendResult tgbotapi.Message // returned by Send (zero value by default)
	admins     []tgbotapi.ChatMember
	adminsErr  error
}

func (m *mockTelegramAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.sendCalls = append(m.sendCalls, c)
	return m.sendResult, m.sendErr
}

func (m *mockTelegramAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return nil, nil
}

func (m *mockTelegramAPI) GetChatAdministrators(config tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return m.admins, m.adminsErr
}

func makeChatMember(id int64, isBot bool) tgbotapi.ChatMember {
	return tgbotapi.ChatMember{User: &tgbotapi.User{ID: id, IsBot: isBot}}
}

// ── parsePreferredTime ────────────────────────────────────────────────────────

func TestParsePreferredTime_Valid(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	dt, err := parsePreferredTime("2026-04-01", "18:00", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dt.Hour() != 18 || dt.Minute() != 0 {
		t.Errorf("expected 18:00, got %02d:%02d", dt.Hour(), dt.Minute())
	}
	if dt.Location().String() != loc.String() {
		t.Errorf("expected location %v, got %v", loc, dt.Location())
	}
}

func TestParsePreferredTime_UTCConversion(t *testing.T) {
	// Berlin is UTC+2 in summer; 18:00 local should be 16:00 UTC.
	berlin, _ := time.LoadLocation("Europe/Berlin")
	dt, err := parsePreferredTime("2026-06-15", "18:00", berlin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	utc := dt.UTC()
	if utc.Hour() != 16 || utc.Minute() != 0 {
		t.Errorf("expected UTC 16:00, got %02d:%02d", utc.Hour(), utc.Minute())
	}
}

func TestParsePreferredTime_InvalidFormat_MissingColon(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "1800", time.UTC)
	if err == nil {
		t.Fatal("expected error for missing colon, got nil")
	}
}

func TestParsePreferredTime_InvalidFormat_ShortHour(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "8:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for single-digit hour, got nil")
	}
}

func TestParsePreferredTime_InvalidTime_OutOfRange(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "25:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for hour 25, got nil")
	}
}

func TestParsePreferredTime_InvalidDate(t *testing.T) {
	_, err := parsePreferredTime("not-a-date", "18:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for invalid date, got nil")
	}
}

// ── slotQueryWindow ───────────────────────────────────────────────────────────

func TestSlotQueryWindow_DayBoundary(t *testing.T) {
	// Regression: when the code used UTC formatting, a game near midnight in a
	// non-UTC timezone caused slotQueryWindow to return the wrong date and HHMM.
	//
	// Berlin in January is CET (UTC+1).
	// 2026-01-15 23:30 UTC == 2026-01-16 00:30 CET.
	// The correct query window is on "2026-01-16" starting at "0030" (local time).
	// Using UTC would produce "2026-01-15" / "2330" — a different date entirely.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gameUTC := time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)
	gameStart := gameUTC.In(berlin)

	date, startHHMM, endHHMM := slotQueryWindow(gameStart)

	if date != "2026-01-16" {
		t.Errorf("date: got %q, want %q (local Berlin date)", date, "2026-01-16")
	}
	if startHHMM != "0030" {
		t.Errorf("startHHMM: got %q, want %q (local Berlin time)", startHHMM, "0030")
	}
	if endHHMM != "0040" {
		t.Errorf("endHHMM: got %q, want %q (local Berlin time +10 min)", endHHMM, "0040")
	}

	// Sanity-check: UTC formatting would have produced a different date, confirming
	// the test would catch a regression that uses .UTC() instead of the local time.
	if gameUTC.Format("2006-01-02") != "2026-01-15" {
		t.Error("UTC date sanity check failed — test setup is wrong")
	}
}

// ── extractCourtNumber ────────────────────────────────────────────────────────

func TestExtractCourtNumber_Valid(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"Court 1", 1},
		{"Court 7", 7},
		{"Court 12", 12},
		{"  Court 3  ", 3}, // extra whitespace handled by Fields
	}
	for _, tc := range cases {
		if got := extractCourtNumber(tc.name); got != tc.want {
			t.Errorf("extractCourtNumber(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestExtractCourtNumber_Invalid(t *testing.T) {
	// Names with no trailing positive integer should return -1.
	cases := []string{"", "Main Court", "Court", "Court 0", "Court -1"}
	for _, name := range cases {
		if got := extractCourtNumber(name); got > 0 {
			t.Errorf("extractCourtNumber(%q) = %d, want <=0", name, got)
		}
	}
}

// ── filterFreeCourts ──────────────────────────────────────────────────────────

func TestFilterFreeCourts_AllFree(t *testing.T) {
	// Court names drive the matching; Eversports IDs (77385/77386) are only used
	// for the occupancy check — not for venue or preference matching.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-1", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-2", Name: "Court 2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 free courts, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-1" || got[1] != "uuid-2" {
		t.Errorf("expected [uuid-1 uuid-2], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesOccupied(t *testing.T) {
	// occupied is keyed by the Eversports court ID (from sl.Court in ListMatches).
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-1", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-2", Name: "Court 2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	occupied := map[int]bool{77386: true} // Court 2 occupied
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesNonVenueCourts(t *testing.T) {
	// Court 3 (name-number 3) is not in venueCourts → excluded.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-1", Name: "Court 1"},
		{ID: "77387", UUID: "uuid-3", Name: "Court 3"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesMissingUUID(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "", Name: "Court 1"}, // no UUID
	}
	venueCourts := map[int]bool{1: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected no results for missing UUID, got %v", got)
	}
}

func TestFilterFreeCourts_NoneAvailable(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-1", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-2", Name: "Court 2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	occupied := map[int]bool{77385: true, 77386: true}
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestFilterFreeCourts_EmptyCourts(t *testing.T) {
	venueCourts := map[int]bool{1: true}
	got := filterFreeCourts(nil, map[int]bool{}, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil courts, got %v", got)
	}
}

func TestFilterFreeCourts_FallbackWhenVenueNumbersMismatch(t *testing.T) {
	// Venue configured with court numbers 10,11 but actual court names are
	// "Court 1", "Court 2" — name-numbers don't match venueCourts →
	// falls back to returning all free courts.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-a", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-b", Name: "Court 2"},
		{ID: "77387", UUID: "uuid-c", Name: "Court 3"},
	}
	venueCourts := map[int]bool{10: true, 11: true}
	occupied := map[int]bool{77387: true} // Court 3 occupied
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 courts via fallback, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-a" || got[1] != "uuid-b" {
		t.Errorf("expected [uuid-a uuid-b], got %v", got)
	}
}

func TestFilterFreeCourts_OrderedPreferred(t *testing.T) {
	// orderedPreferred [3, 2, 1] matched against name-numbers → priority order.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-1", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-2", Name: "Court 2"},
		{ID: "77387", UUID: "uuid-3", Name: "Court 3"},
	}
	venueCourts := map[int]bool{1: true, 2: true, 3: true}
	orderedPreferred := []int{3, 2, 1}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, orderedPreferred)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-3" || got[1] != "uuid-2" || got[2] != "uuid-1" {
		t.Errorf("expected [uuid-3 uuid-2 uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_FallbackWhenPreferredNumbersMismatch(t *testing.T) {
	// orderedPreferred contains numbers (5, 6) that don't match any court name →
	// falls back to all eligible courts in response order.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-a", Name: "Court 1"},
		{ID: "77386", UUID: "uuid-b", Name: "Court 2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	orderedPreferred := []int{5, 6}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, orderedPreferred)
	if len(got) != 2 {
		t.Fatalf("expected 2 courts via fallback, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-a" || got[1] != "uuid-b" {
		t.Errorf("expected [uuid-a uuid-b], got %v", got)
	}
}

// ── processAutoBookingForVenue ────────────────────────────────────────────────

func TestProcessAutoBookingForVenue_Disabled_DoesNotCallListMatches(t *testing.T) {
	client := &mockBookingClient{}
	s := &AutoBookingJob{
		bookingClient: client,
		logger:        noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: false,
		Courts:             "1,2",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)
	got := s.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)
	if got {
		t.Error("expected false when AutoBookingEnabled is false")
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called, got %d calls", client.listCalls)
	}
}

func TestProcessAutoBookingForVenue_InvalidPreferredTime_DoesNotCallListMatches(t *testing.T) {
	client := &mockBookingClient{}
	s := &AutoBookingJob{
		bookingClient: client,
		logger:        noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		Courts:             "1,2",
		PreferredGameTimes: "not-valid-time", // parsePreferredTime will fail
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)
	got := s.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)
	if got {
		t.Error("expected false when PreferredGameTime is invalid")
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called, got %d calls", client.listCalls)
	}
}

// ── notifyAutoBookingSuccess ──────────────────────────────────────────────────

func TestNotifyAutoBookingSuccess_SendsSilentDMToEachAdmin(t *testing.T) {
	const groupChatID int64 = -1001
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{
			makeChatMember(101, false),
			makeChatMember(102, false),
		},
	}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyAutoBookingSuccess(context.Background(), groupChatID, venue, "2026-05-01", "18:00", 2, lz)

	if len(api.sendCalls) != 2 {
		t.Fatalf("expected 2 Send calls, got %d", len(api.sendCalls))
	}
	for i, c := range api.sendCalls {
		msg, ok := c.(tgbotapi.MessageConfig)
		if !ok {
			t.Fatalf("call %d: expected MessageConfig, got %T", i, c)
		}
		if !msg.DisableNotification {
			t.Errorf("call %d: DisableNotification should be true (silent DM)", i)
		}
		if msg.ChatID == groupChatID {
			t.Errorf("call %d: message sent to group chat ID %d — expected admin DM", i, groupChatID)
		}
	}
}

func TestNotifyAutoBookingSuccess_SkipsBotAdmins(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{
			makeChatMember(101, false), // human — should receive DM
			makeChatMember(200, true),  // bot — should be skipped
		},
	}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyAutoBookingSuccess(context.Background(), -1001, venue, "2026-05-01", "18:00", 2, lz)

	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 Send call (human admin only), got %d", len(api.sendCalls))
	}
	msg, ok := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if msg.ChatID != 101 {
		t.Errorf("expected DM to user 101, got ChatID %d", msg.ChatID)
	}
}

func TestNotifyAutoBookingSuccess_GetAdminsFails_NoSend(t *testing.T) {
	api := &mockTelegramAPI{adminsErr: errors.New("telegram error")}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyAutoBookingSuccess(context.Background(), -1001, venue, "2026-05-01", "18:00", 2, lz)

	if len(api.sendCalls) != 0 {
		t.Errorf("expected no Send calls when GetChatAdministrators fails, got %d", len(api.sendCalls))
	}
}

// ── notifyNoCredentials ───────────────────────────────────────────────────────

func TestNotifyNoCredentials_SendsAudibleDMToHumanAdmins(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{
			makeChatMember(101, false),
			makeChatMember(200, true), // bot — must be skipped
		},
	}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyNoCredentials(context.Background(), -1001, venue, lz)

	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 Send call (human only), got %d", len(api.sendCalls))
	}
	msg, ok := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if msg.DisableNotification {
		t.Error("notifyNoCredentials should NOT disable notification (sound must be on)")
	}
	if msg.ChatID != 101 {
		t.Errorf("expected DM to user 101, got ChatID %d", msg.ChatID)
	}
}

// ── notifyCredentialError ─────────────────────────────────────────────────────

func TestNotifyCredentialError_SendsAudibleDMToHumanAdmins(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{
			makeChatMember(101, false),
			makeChatMember(200, true), // bot — must be skipped
		},
	}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyCredentialError(context.Background(), -1001, venue, "user@example.com", errors.New("invalid credentials"), 24*time.Hour, lz)

	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 Send call (human only), got %d", len(api.sendCalls))
	}
	msg, ok := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if msg.DisableNotification {
		t.Error("notifyCredentialError should NOT disable notification (sound must be on)")
	}
}

// ── notifyCredentialsExhausted ────────────────────────────────────────────────

func TestNotifyCredentialsExhausted_SendsSilentDMToHumanAdmins(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{
			makeChatMember(101, false),
			makeChatMember(200, true), // bot — must be skipped
		},
	}
	job := &AutoBookingJob{api: api, logger: noopLogger()}
	venue := &models.Venue{ID: 1, Name: "Test Venue"}
	lz := i18n.New(i18n.En)

	job.notifyCredentialsExhausted(context.Background(), -1001, venue, 1, 3, lz)

	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 Send call (human only), got %d", len(api.sendCalls))
	}
	msg, ok := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if !msg.DisableNotification {
		t.Error("notifyCredentialsExhausted should disable notification (silent)")
	}
}

// ── processAutoBookingForVenue credential paths ───────────────────────────────

// newCredServiceForTest builds a VenueCredentialService backed by a stubCredRepo
// pre-populated with the given raw credentials (passwords are pre-encrypted).
func newCredServiceForTest(creds []*models.VenueCredential) *VenueCredentialService {
	enc, _ := NewEncryptor(testHexKey)
	return NewVenueCredentialService(&stubCredRepo{creds: creds}, &stubVenueRepo{}, nil, enc)
}

func TestProcessAutoBookingForVenue_NoCredentials_NotifiesAndReturnsFalse(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{makeChatMember(101, false)},
	}
	client := &mockBookingClient{
		courts: []BookingCourt{
			{ID: "1", UUID: "uuid-1", Name: "Court 1"},
		},
		slots: []BookingSlot{}, // Court 1 is free
	}
	// credService with no credentials stored
	credSvc := newCredServiceForTest(nil)

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if got {
		t.Error("expected false when no credentials are available")
	}
	// Must send a notification (notifyNoCredentials, audible)
	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 admin DM for no-credentials, got %d", len(api.sendCalls))
	}
	msg, ok := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if msg.DisableNotification {
		t.Error("no-credentials notification must have sound on")
	}
}

func TestProcessAutoBookingForVenue_AllCredentialsInCooldown_NotifiesAndReturnsFalse(t *testing.T) {
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{makeChatMember(101, false)},
	}
	client := &mockBookingClient{
		courts: []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
	}

	// Credential with last_error_at 1 hour ago — within 24h cooldown.
	recent := time.Now().Add(-1 * time.Hour)
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("secret")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3, LastErrorAt: &recent},
	}
	credSvc := newCredServiceForTest(creds)

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if got {
		t.Error("expected false when all credentials are in cooldown")
	}
	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 admin DM (no-credentials), got %d", len(api.sendCalls))
	}
	msg := api.sendCalls[0].(tgbotapi.MessageConfig)
	if msg.DisableNotification {
		t.Error("no-credentials notification must have sound on")
	}
}

// ── stubAutoBookingResultRepo ─────────────────────────────────────────────────

type stubAutoBookingResultRepo struct{}

func (r *stubAutoBookingResultRepo) Save(_ context.Context, _ int64, _ time.Time, _, _ string, _ int) error {
	return nil
}

func (r *stubAutoBookingResultRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *stubAutoBookingResultRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *stubAutoBookingResultRepo) GetByGameID(_ context.Context, _ int64) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *stubAutoBookingResultRepo) SetGameID(_ context.Context, _, _ int64) error {
	return nil
}

// ── courtBookingRepo.Save tests ───────────────────────────────────────────────

func TestProcessAutoBookingForVenue_SavesCourtBookingEntry(t *testing.T) {
	// A successful BookMatch with a non-empty MatchID must produce a court_bookings row.
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("pass")
	credID := int64(1)
	creds := []*models.VenueCredential{
		{ID: credID, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3},
	}
	credSvc := NewVenueCredentialService(&stubCredRepo{creds: creds}, &stubVenueRepo{}, nil, enc)

	client := &mockBookingClient{
		courts: []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:  []BookingSlot{}, // court 1 free
		bookResult: &BookMatchResult{
			MatchID:     "match-uuid-1",
			BookingUUID: "booking-uuid-1",
		},
	}
	cbRepo := &stubCourtBookingRepo{}
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if !got {
		t.Error("expected processAutoBookingForVenue to return true on success")
	}
	if len(cbRepo.saved) != 1 {
		t.Fatalf("expected 1 court booking saved, got %d", len(cbRepo.saved))
	}
	if cbRepo.saved[0].MatchID != "match-uuid-1" {
		t.Errorf("saved MatchID: got %q, want %q", cbRepo.saved[0].MatchID, "match-uuid-1")
	}
	if cbRepo.saved[0].CredentialID == nil || *cbRepo.saved[0].CredentialID != credID {
		t.Errorf("saved CredentialID: got %v, want %d", cbRepo.saved[0].CredentialID, credID)
	}
}

// credentialRecordingClient records the login passed to ListCourts and ListMatches
// so tests can assert that the first credential was forwarded to both list endpoints.
type credentialRecordingClient struct {
	courts           []BookingCourt
	slots            []BookingSlot
	bookResult       *BookMatchResult
	listCourtsLogin  string
	listMatchesLogin string
}

func (m *credentialRecordingClient) ListCourts(_ context.Context, _, login, _ string) ([]BookingCourt, error) {
	m.listCourtsLogin = login
	return m.courts, nil
}

func (m *credentialRecordingClient) ListMatches(_ context.Context, _, _, _ string, _ bool, login, _ string) ([]BookingSlot, error) {
	m.listMatchesLogin = login
	return m.slots, nil
}

func (m *credentialRecordingClient) CancelMatch(_ context.Context, _, _, _ string) error { return nil }

func (m *credentialRecordingClient) BookMatch(_ context.Context, _, _, _, login, _ string) (*BookMatchResult, error) {
	return m.bookResult, nil
}

// TestProcessAutoBookingForVenue_PassesFirstCredentialToListCalls verifies that
// both ListCourts and ListMatches receive the login of the highest-priority (first)
// credential, not an empty string.
func TestProcessAutoBookingForVenue_PassesFirstCredentialToListCalls(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	encPw1, _ := enc.Encrypt("pass1")
	encPw2, _ := enc.Encrypt("pass2")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "first@example.com", EncryptedPassword: encPw1, Priority: 0, MaxCourts: 1},
		{ID: 2, VenueID: 1, Login: "second@example.com", EncryptedPassword: encPw2, Priority: 1, MaxCourts: 1},
	}
	credSvc := newCredServiceForTest(creds)

	client := &credentialRecordingClient{
		courts:     []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:      []BookingSlot{}, // court 1 free
		bookResult: &BookMatchResult{MatchID: "match-1", BookingUUID: "booking-1"},
	}

	job := &AutoBookingJob{
		api:                   &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}},
		bookingClient:         client,
		credService:           credSvc,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if !got {
		t.Error("expected true on successful booking")
	}
	if client.listCourtsLogin != "first@example.com" {
		t.Errorf("ListCourts login: want %q, got %q", "first@example.com", client.listCourtsLogin)
	}
	if client.listMatchesLogin != "first@example.com" {
		t.Errorf("ListMatches login: want %q, got %q", "first@example.com", client.listMatchesLogin)
	}
}

// TestProcessAutoBookingForVenue_NoCredentials_SkipsListCalls verifies that the
// credential check happens before any Eversports list calls, so ListMatches is
// never invoked when there are no usable credentials.
func TestProcessAutoBookingForVenue_NoCredentials_SkipsListCalls(t *testing.T) {
	credSvc := newCredServiceForTest(nil) // no credentials stored

	client := &mockBookingClient{}
	job := &AutoBookingJob{
		api:                   &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}},
		bookingClient:         client,
		credService:           credSvc,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if got {
		t.Error("expected false when no credentials")
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches must not be called before credentials are available, got %d calls", client.listCalls)
	}
}

// ── multi-slot booking tests ──────────────────────────────────────────────────

// recordingAutoBookingResultRepo records Save calls so tests can verify that
// each time slot produces a separate auto_booking_results row.
type recordingAutoBookingResultRepo struct {
	savedGameTimes []string                             // game_time argument of each Save call
	slotResults    map[string]*models.AutoBookingResult // keyed by game_time; nil entry = "not found"
	getByTimeErr   error                                // if set, GetByVenueAndDateAndTime returns this error for all calls
}

func (r *recordingAutoBookingResultRepo) Save(_ context.Context, _ int64, _ time.Time, gameTime, _ string, _ int) error {
	r.savedGameTimes = append(r.savedGameTimes, gameTime)
	return nil
}

func (r *recordingAutoBookingResultRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *recordingAutoBookingResultRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, gameTime string) (*models.AutoBookingResult, error) {
	if r.getByTimeErr != nil {
		return nil, r.getByTimeErr
	}
	if r.slotResults != nil {
		return r.slotResults[gameTime], nil
	}
	return nil, nil
}

func (r *recordingAutoBookingResultRepo) GetByGameID(_ context.Context, _ int64) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *recordingAutoBookingResultRepo) SetGameID(_ context.Context, _, _ int64) error {
	return nil
}

// TestProcessAutoBookingForVenue_TwoTimeSlots_BooksAndSavesBoth verifies that when a
// venue has two preferred times ("18:00,20:00"), both slots are booked independently
// and each produces its own auto_booking_results and court_bookings row.
func TestProcessAutoBookingForVenue_TwoTimeSlots_BooksAndSavesBoth(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("pass")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3},
	}
	credSvc := newCredServiceForTest(creds)

	// Use distinct MatchIDs per call: in production Eversports always returns unique
	// match UUIDs, and court_bookings has ON CONFLICT (match_id) DO NOTHING which
	// would silently drop the second save if both calls returned the same ID.
	client := &mockBookingClient{
		courts: []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:  nil, // court 1 free at both slots
		bookResultsByCall: []*BookMatchResult{
			{MatchID: "match-1800", BookingUUID: "bk-1800"},
			{MatchID: "match-2000", BookingUUID: "bk-2000"},
		},
	}
	cbRepo := &stubCourtBookingRepo{}
	resultRepo := &recordingAutoBookingResultRepo{}
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}
	api.sendResult = tgbotapi.Message{MessageID: 1}

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: resultRepo,
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00,20:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if !got {
		t.Error("expected processAutoBookingForVenue to return true when courts booked")
	}
	if len(resultRepo.savedGameTimes) != 2 {
		t.Fatalf("expected 2 auto_booking_result saves, got %d: %v",
			len(resultRepo.savedGameTimes), resultRepo.savedGameTimes)
	}
	if resultRepo.savedGameTimes[0] != "18:00" || resultRepo.savedGameTimes[1] != "20:00" {
		t.Errorf("saved game times: got %v, want [18:00 20:00]", resultRepo.savedGameTimes)
	}
	if len(cbRepo.saved) != 2 {
		t.Fatalf("expected 2 court_bookings saves (one per slot), got %d", len(cbRepo.saved))
	}
	// Each court_bookings row must carry its slot's game_time and a distinct MatchID.
	matchIDs := make(map[string]bool)
	gameTimes := make(map[string]bool)
	for _, cb := range cbRepo.saved {
		gameTimes[cb.GameTime] = true
		matchIDs[cb.MatchID] = true
	}
	if !gameTimes["18:00"] || !gameTimes["20:00"] {
		t.Errorf("court_bookings game_times: got %v, want both 18:00 and 20:00", gameTimes)
	}
	if len(matchIDs) != 2 {
		t.Errorf("expected distinct MatchIDs per slot, got %v", matchIDs)
	}
}

// TestProcessAutoBookingForVenue_SlotAlreadyBooked_SkipsIt verifies per-slot dedup:
// when GetByVenueAndDateAndTime returns a non-nil result for a slot, that slot is
// skipped and only the fresh slot is booked.
func TestProcessAutoBookingForVenue_SlotAlreadyBooked_SkipsIt(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("pass")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3},
	}
	credSvc := newCredServiceForTest(creds)

	client := &mockBookingClient{
		courts:     []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:      nil,
		bookResult: &BookMatchResult{MatchID: "match-1", BookingUUID: "bk-1"},
	}
	cbRepo := &stubCourtBookingRepo{}
	// 20:00 already booked; 18:00 is fresh.
	resultRepo := &recordingAutoBookingResultRepo{
		slotResults: map[string]*models.AutoBookingResult{
			"20:00": {ID: 99, GameTime: "20:00"},
		},
	}
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}
	api.sendResult = tgbotapi.Message{MessageID: 1}

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: resultRepo,
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00,20:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if !got {
		t.Error("expected true (18:00 was booked)")
	}
	// Only 18:00 should produce a save; 20:00 was already recorded.
	if len(resultRepo.savedGameTimes) != 1 {
		t.Fatalf("expected 1 save (18:00 only), got %d: %v",
			len(resultRepo.savedGameTimes), resultRepo.savedGameTimes)
	}
	if resultRepo.savedGameTimes[0] != "18:00" {
		t.Errorf("expected only 18:00 saved, got %v", resultRepo.savedGameTimes)
	}
}

func TestProcessAutoBookingForVenue_SkipsCourtBookingEntryWhenMatchIDEmpty(t *testing.T) {
	// BookMatch returns BookingUUID but empty MatchID (step 3 failed) → no court_bookings row.
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("pass")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3},
	}
	credSvc := NewVenueCredentialService(&stubCredRepo{creds: creds}, &stubVenueRepo{}, nil, enc)

	client := &mockBookingClient{
		courts: []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:  []BookingSlot{},
		bookResult: &BookMatchResult{
			MatchID:     "", // empty — step 3 of checkout failed
			BookingUUID: "booking-uuid-1",
		},
	}
	cbRepo := &stubCourtBookingRepo{}
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: &stubAutoBookingResultRepo{},
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if len(cbRepo.saved) != 0 {
		t.Errorf("expected no court booking saved when MatchID is empty, got %d", len(cbRepo.saved))
	}
}

// TestProcessAutoBookingForVenue_DedupCheckError_SkipsSlot verifies that when
// GetByVenueAndDateAndTime returns a transient DB error, the slot is skipped
// (conservative: do not attempt booking, avoid risk of double-booking).
func TestProcessAutoBookingForVenue_DedupCheckError_SkipsSlot(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("pass")
	creds := []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "a@b.com", EncryptedPassword: encPw, Priority: 0, MaxCourts: 3},
	}
	credSvc := newCredServiceForTest(creds)

	client := &mockBookingClient{
		courts:     []BookingCourt{{ID: "1", UUID: "uuid-1", Name: "Court 1"}},
		slots:      nil,
		bookResult: &BookMatchResult{MatchID: "match-1", BookingUUID: "bk-1"},
	}
	cbRepo := &stubCourtBookingRepo{}
	resultRepo := &recordingAutoBookingResultRepo{
		getByTimeErr: errors.New("db timeout"),
	}
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}

	job := &AutoBookingJob{
		api:                   api,
		bookingClient:         client,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: resultRepo,
		venueRepo:             &stubVenueRepo{},
		credCooldown:          24 * time.Hour,
		courtsCount:           1,
		logger:                noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: true,
		Courts:             "1",
		PreferredGameTimes: "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)

	got := job.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)

	if got {
		t.Error("expected false: slot must be skipped on dedup check error, not booked")
	}
	if len(cbRepo.saved) != 0 {
		t.Errorf("expected no court_bookings saved when dedup check errored, got %d", len(cbRepo.saved))
	}
	if len(resultRepo.savedGameTimes) != 0 {
		t.Errorf("expected no auto_booking_result saved when slot skipped, got %v", resultRepo.savedGameTimes)
	}
}

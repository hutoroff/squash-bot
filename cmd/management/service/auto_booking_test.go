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
	sendCalls []tgbotapi.Chattable
	sendErr   error
	admins    []tgbotapi.ChatMember
	adminsErr error
}

func (m *mockTelegramAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.sendCalls = append(m.sendCalls, c)
	return tgbotapi.Message{}, m.sendErr
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
		PreferredGameTime:  "18:00",
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
		ID:                1,
		Courts:            "1,2",
		PreferredGameTime: "not-valid-time", // parsePreferredTime will fail
		BookingOpensDays:  14,
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

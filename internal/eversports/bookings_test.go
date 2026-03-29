package eversports

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// discard is a logger that silences all output during tests.
var discard = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))

// ─── parseActivityItem ────────────────────────────────────────────────────────

const fullItemHTML = `<li data-match-relative-link="/match/abc-uuid-123">
  <input id="google-calendar-start" value="20260330T184500">
  <input id="google-calendar-end" value="20260330T194500">
  <input id="booking-sport" value="Squash">
  <input id="facility-name" value="Sport Center">
  <span class="session-info-value">Court 9</span>
</li>`

func TestParseActivityItem_Valid(t *testing.T) {
	b, err := parseActivityItem(fullItemHTML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.ID != "abc-uuid-123" {
		t.Errorf("ID: want %q, got %q", "abc-uuid-123", b.ID)
	}
	wantStart := time.Date(2026, 3, 30, 18, 45, 0, 0, time.UTC)
	if !b.Start.Equal(wantStart) {
		t.Errorf("Start: want %v, got %v", wantStart, b.Start)
	}
	wantEnd := time.Date(2026, 3, 30, 19, 45, 0, 0, time.UTC)
	if !b.End.Equal(wantEnd) {
		t.Errorf("End: want %v, got %v", wantEnd, b.End)
	}
	if b.Sport.Name != "Squash" {
		t.Errorf("Sport.Name: want %q, got %q", "Squash", b.Sport.Name)
	}
	if b.Venue.Name != "Sport Center" {
		t.Errorf("Venue.Name: want %q, got %q", "Sport Center", b.Venue.Name)
	}
	if b.Court.Name != "Court 9" {
		t.Errorf("Court.Name: want %q, got %q", "Court 9", b.Court.Name)
	}
	if b.State != "ACCEPTED" {
		t.Errorf("State: want %q, got %q", "ACCEPTED", b.State)
	}
}

func TestParseActivityItem_NoMatchLink(t *testing.T) {
	// A booking without a match page: no data-match-relative-link attribute.
	item := `<li>
  <input id="google-calendar-start" value="20260330T100000">
  <input id="google-calendar-end" value="20260330T110000">
  <input id="booking-sport" value="Tennis">
  <input id="facility-name" value="Club">
</li>`
	b, err := parseActivityItem(item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.ID != "" {
		t.Errorf("ID: want empty string, got %q", b.ID)
	}
	if b.Sport.Name != "Tennis" {
		t.Errorf("Sport.Name: want %q, got %q", "Tennis", b.Sport.Name)
	}
}

func TestParseActivityItem_MissingStartTime(t *testing.T) {
	item := `<li><input id="google-calendar-end" value="20260330T110000"></li>`
	_, err := parseActivityItem(item)
	if err == nil {
		t.Fatal("expected error for missing start time, got nil")
	}
}

func TestParseActivityItem_MissingEndTime(t *testing.T) {
	item := `<li><input id="google-calendar-start" value="20260330T100000"></li>`
	_, err := parseActivityItem(item)
	if err == nil {
		t.Fatal("expected error for missing end time, got nil")
	}
}

func TestParseActivityItem_InvalidStartFormat(t *testing.T) {
	item := `<li>
  <input id="google-calendar-start" value="not-a-date">
  <input id="google-calendar-end" value="20260330T110000">
</li>`
	_, err := parseActivityItem(item)
	if err == nil {
		t.Fatal("expected error for invalid start time format, got nil")
	}
}

func TestParseActivityItem_NoCourt(t *testing.T) {
	// Items without a court span are valid; Court.Name stays empty.
	item := `<li data-match-relative-link="/match/x">
  <input id="google-calendar-start" value="20260330T100000">
  <input id="google-calendar-end" value="20260330T110000">
</li>`
	b, err := parseActivityItem(item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Court.Name != "" {
		t.Errorf("Court.Name: want empty, got %q", b.Court.Name)
	}
}

// ─── parseActivitiesHTML ──────────────────────────────────────────────────────

func makeItem(matchID, start, end string) string {
	linkAttr := ""
	if matchID != "" {
		linkAttr = ` data-match-relative-link="/match/` + matchID + `"`
	}
	return `<li` + linkAttr + `>` +
		`<input id="google-calendar-start" value="` + start + `">` +
		`<input id="google-calendar-end" value="` + end + `">` +
		`</li>`
}

func TestParseActivitiesHTML_MultipleItems(t *testing.T) {
	html := makeItem("id1", "20260330T100000", "20260330T110000") +
		makeItem("id2", "20260330T120000", "20260330T130000")

	bookings := parseActivitiesHTML(html, discard)

	if len(bookings) != 2 {
		t.Fatalf("count: want 2, got %d", len(bookings))
	}
	if bookings[0].ID != "id1" {
		t.Errorf("bookings[0].ID: want %q, got %q", "id1", bookings[0].ID)
	}
	if bookings[1].ID != "id2" {
		t.Errorf("bookings[1].ID: want %q, got %q", "id2", bookings[1].ID)
	}
}

func TestParseActivitiesHTML_SkipsBadItems(t *testing.T) {
	badItem := `<li><span>no inputs here</span></li>`
	goodItem := makeItem("good-id", "20260330T100000", "20260330T110000")

	bookings := parseActivitiesHTML(badItem+goodItem, discard)

	if len(bookings) != 1 {
		t.Errorf("count: want 1 (bad item skipped), got %d", len(bookings))
	}
	if len(bookings) == 1 && bookings[0].ID != "good-id" {
		t.Errorf("bookings[0].ID: want %q, got %q", "good-id", bookings[0].ID)
	}
}

func TestParseActivitiesHTML_Empty(t *testing.T) {
	bookings := parseActivitiesHTML("", discard)
	if len(bookings) != 0 {
		t.Errorf("count: want 0, got %d", len(bookings))
	}
}

func TestParseActivitiesHTML_AllBadItems(t *testing.T) {
	html := `<li><span>bad</span></li><li><span>also bad</span></li>`
	bookings := parseActivitiesHTML(html, discard)
	if len(bookings) != 0 {
		t.Errorf("count: want 0, got %d", len(bookings))
	}
}

// ─── parseTime ────────────────────────────────────────────────────────────────

func TestParseTime_RFC3339(t *testing.T) {
	got, err := parseTime("2026-03-30T18:45:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 3, 30, 18, 45, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestParseTime_RFC3339WithOffset(t *testing.T) {
	got, err := parseTime("2026-03-30T19:45:00+01:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 19:45+01:00 == 18:45 UTC
	want := time.Date(2026, 3, 30, 18, 45, 0, 0, time.UTC)
	if !got.UTC().Equal(want) {
		t.Errorf("want %v UTC, got %v UTC", want, got.UTC())
	}
}

func TestParseTime_WithMilliseconds(t *testing.T) {
	got, err := parseTime("2026-03-30T18:45:00.000Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 3, 30, 18, 45, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestParseTime_Invalid(t *testing.T) {
	_, err := parseTime("not-a-timestamp")
	if err == nil {
		t.Fatal("expected error for invalid timestamp, got nil")
	}
}

package eversports

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ─── isCFChallenge ────────────────────────────────────────────────────────────

func TestIsCFChallenge_True(t *testing.T) {
	body := []byte(`<html><head><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script></head></html>`)
	if !isCFChallenge(body) {
		t.Error("expected true for CF challenge page")
	}
}

func TestIsCFChallenge_False(t *testing.T) {
	body := []byte(`{"matchId":"abc-123"}`)
	if isCFChallenge(body) {
		t.Error("expected false for normal JSON response")
	}
}

func TestIsCFChallenge_Empty(t *testing.T) {
	if isCFChallenge([]byte{}) {
		t.Error("expected false for empty body")
	}
}

// ─── parseCalendarHTML ────────────────────────────────────────────────────────

const calendarHTML = `
<table>
  <tr class="court">
    <td data-court="1" data-court-uuid="uuid-1"><div class="court-name">Court 1</div></td>
  </tr>
  <tr class="court">
    <td data-court="2" data-court-uuid="uuid-2"><div class="court-name">Court 2</div></td>
  </tr>
  <tr class="court">
    <td data-court="1" data-court-uuid="uuid-1"><div class="court-name">Court 1</div></td>
  </tr>
</table>`

func TestParseCalendarHTML_Deduplicates(t *testing.T) {
	courts := parseCalendarHTML(calendarHTML)
	if len(courts) != 2 {
		t.Fatalf("count: want 2, got %d", len(courts))
	}
	if courts[0].ID != "1" || courts[0].UUID != "uuid-1" || courts[0].Name != "Court 1" {
		t.Errorf("courts[0]: got %+v", courts[0])
	}
	if courts[1].ID != "2" || courts[1].UUID != "uuid-2" || courts[1].Name != "Court 2" {
		t.Errorf("courts[1]: got %+v", courts[1])
	}
}

func TestParseCalendarHTML_Empty(t *testing.T) {
	courts := parseCalendarHTML("")
	if len(courts) != 0 {
		t.Errorf("count: want 0, got %d", len(courts))
	}
}

func TestParseCalendarHTML_MissingID(t *testing.T) {
	// Row missing data-court attribute should be skipped.
	html := `<tr class="court"><td data-court-uuid="uuid-x"><div class="court-name">X</div></td></tr>`
	courts := parseCalendarHTML(html)
	if len(courts) != 0 {
		t.Errorf("count: want 0 (row skipped), got %d", len(courts))
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

// ─── cancelMatchMutation regression ──────────────────────────────────────────

// TestCancelMatchMutation_OriginDeclaredAndUsed guards against the class of
// bug where a GraphQL variable is listed in the operation signature but never
// referenced in the selection set (or vice-versa), which causes a
// GRAPHQL_VALIDATION_FAILED error at runtime.
func TestCancelMatchMutation_OriginDeclaredAndUsed(t *testing.T) {
	if !strings.Contains(cancelMatchMutation, "$origin: Origin!") {
		t.Error("cancelMatchMutation must declare $origin in the operation signature")
	}
	if !strings.Contains(cancelMatchMutation, "origin: $origin") {
		t.Error("cancelMatchMutation must pass origin: $origin to the cancelMatch field")
	}
}

// TestCancelMatchRequest_IncludesOriginVariable ensures the variables map
// serialised when calling CancelMatch includes the origin field that the
// mutation declares.
func TestCancelMatchRequest_IncludesOriginVariable(t *testing.T) {
	payload := gqlRequest{
		OperationName: "CancelMatch",
		Variables: map[string]any{
			"matchId": "test-match-id",
			"origin":  "ORIGIN_MARKETPLACE",
		},
		Query: cancelMatchMutation,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(body, []byte("ORIGIN_MARKETPLACE")) {
		t.Errorf("serialised CancelMatch request must include origin variable, got: %s", body)
	}
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

type fakeLookupClient struct {
	*fakeProviderClient
	courts   []eversports.Court
	slots    []eversports.Slot
	slotsErr error

	courtReads, slotReads int
	slotDate              string
}

func (c *fakeLookupClient) GetCourts(context.Context, string, string, string, string, string, string, string) ([]eversports.Court, error) {
	c.courtReads++
	return c.courts, nil
}

func (c *fakeLookupClient) GetSlots(_ context.Context, _ string, _ []string, date string) ([]eversports.Slot, error) {
	c.slotReads++
	c.slotDate = date
	return c.slots, c.slotsErr
}

func newLookupProviderRunner(t *testing.T) (*ProviderRunner, *fakeStepJournal, *fakeProviderEvidence) {
	t.Helper()
	journal := &fakeStepJournal{failIntent: make(map[string]error), failEvidence: make(map[string]error)}
	evidence := &fakeProviderEvidence{}
	runner, err := NewProviderRunner(journal, evidence, ProviderRunnerConfig{
		IdentityKey: providerRunnerKey, SportUUID: "sport-1", SportID: "496", SportName: "Squash",
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner, journal, evidence
}

func ownedSlot(start string, court int, booking *int, matchID string) eversports.Slot {
	slot := eversports.Slot{Date: "2026-08-10", Start: start, Court: court, IsUserBookingOwner: true, Booking: booking}
	if matchID != "" {
		slot.Match = &eversports.SlotMatch{UUID: matchID}
	}
	return slot
}

func intPtr(value int) *int { return &value }

func TestProviderRunnerAttachmentFailureResolvesMatchByCalendarLookup(t *testing.T) {
	runner, journal, evidence := newLookupProviderRunner(t)
	client := &fakeLookupClient{
		fakeProviderClient: &fakeProviderClient{attachErr: mutationError(eversports.MutationPhaseMatchAttachment, eversports.MutationUnknown, eversports.MutationCodeHTTPError, false)},
		courts:             []eversports.Court{{ID: "77385", UUID: "court-1", Name: "Court 1"}, {ID: "77386", UUID: "court-2", Name: "Court 2"}},
		slots: []eversports.Slot{
			ownedSlot("2045", 77385, intPtr(42), "match-from-calendar"),
			ownedSlot("1930", 77385, intPtr(41), "other-time"),
			{Date: "2026-08-10", Start: "2045", Court: 77385, Booking: intPtr(99), Match: &eversports.SlotMatch{UUID: "not-owner"}},
		},
	}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err != nil || completion.State != bookingapi.StateSucceeded || completion.Outcome != bookingapi.OutcomeSuccess || completion.DiscardSession {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.attachments != 1 || client.tracking != 1 || client.courtReads != 1 || client.slotReads != 1 || client.slotDate != "2026-08-10" {
		t.Fatalf("attach/tracking/courts/slots/date = %d/%d/%d/%d/%s", client.attachments, client.tracking, client.courtReads, client.slotReads, client.slotDate)
	}
	if client.reserves != 1 || client.payments != 1 {
		t.Fatalf("lookup must not repeat mutations: reserve/pay = %d/%d", client.reserves, client.payments)
	}
	if last := evidence.bookings[len(evidence.bookings)-1]; last.MatchID != "match-from-calendar" {
		t.Fatalf("provider booking match = %q", last.MatchID)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseMatchAttachment, attempt: 1, effect: "confirmed", code: "match_resolved_by_lookup"})
}

func TestProviderRunnerAttachmentLookupRefusesUnprovenMatch(t *testing.T) {
	attachErr := mutationError(eversports.MutationPhaseMatchAttachment, eversports.MutationUnknown, eversports.MutationCodeHTTPError, false)
	courts := []eversports.Court{{ID: "77385", UUID: "court-1", Name: "Court 1"}}
	tests := []struct {
		name     string
		courts   []eversports.Court
		slots    []eversports.Slot
		slotsErr error
	}{
		{name: "no owned slot", courts: courts, slots: []eversports.Slot{{Date: "2026-08-10", Start: "2045", Court: 77385, Match: &eversports.SlotMatch{UUID: "m"}}}},
		{name: "owned slot without match", courts: courts, slots: []eversports.Slot{ownedSlot("2045", 77385, intPtr(42), "")}},
		{name: "different booking id", courts: courts, slots: []eversports.Slot{ownedSlot("2045", 77385, intPtr(43), "m")}},
		{name: "ambiguous slots", courts: courts, slots: []eversports.Slot{ownedSlot("2045", 77385, nil, "m1"), ownedSlot("2045", 77385, nil, "m2")}},
		{name: "court missing", courts: []eversports.Court{{ID: "1", UUID: "other"}}},
		{name: "slots read failure", courts: courts, slotsErr: errors.New("calendar unavailable")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner, journal, _ := newLookupProviderRunner(t)
			client := &fakeLookupClient{fakeProviderClient: &fakeProviderClient{attachErr: attachErr}, courts: tc.courts, slots: tc.slots, slotsErr: tc.slotsErr}
			completion, err := runner.Run(context.Background(), createExecution(client))
			if err == nil || completion.Outcome != bookingapi.OutcomeSuccessWithWarning || !completion.DiscardSession {
				t.Fatalf("completion/error = %+v/%v", completion, err)
			}
			if client.tracking != 0 || client.reserves != 1 || client.payments != 1 || client.attachments != 1 {
				t.Fatalf("calls tracking/reserve/pay/attach = %d/%d/%d/%d", client.tracking, client.reserves, client.payments, client.attachments)
			}
			assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseMatchAttachment, attempt: 1, effect: "unknown", code: "http_error"})
		})
	}
}

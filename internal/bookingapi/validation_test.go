package bookingapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	testOperationID = "0195f8db-7a6f-7c7b-8f71-0f6e0712042d"
	testParentID    = "550e8400-e29b-41d4-a716-446655440000"
)

var testIdentityKey = []byte("0123456789abcdef0123456789abcdef")

var testScope = Scope{
	ServiceKey:   "booking.eversports.primary",
	Provider:     "eversports",
	FacilityID:   "76443",
	FacilityUUID: "6266968c-b0fd-4115-ad3b-ae225cc880f1",
	FacilitySlug: "squash-house-berlin-03",
	Sport:        "squash",
}

func validCreateRequest() CreateRequest {
	return CreateRequest{
		CourtUUID: "32ef2369-cf50-427f-8bdf-d380189584e8",
		Start:     "2026-04-12T08:45:00+02:00",
		End:       "2026-04-12T09:30:00+02:00",
		Email:     "Player@example.com",
		Password:  "first-secret",
	}
}

func TestParseOperationIDCanonicalizesAndValidatesRFC4122(t *testing.T) {
	got, err := ParseOperationID(strings.ToUpper(testOperationID))
	if err != nil {
		t.Fatalf("ParseOperationID: %v", err)
	}
	if got != OperationID(testOperationID) {
		t.Fatalf("operation ID = %q, want %q", got, testOperationID)
	}

	invalidIDs := []string{
		"",
		" " + testOperationID,
		"00000000-0000-0000-0000-000000000000",
		"0195f8db-7a6f-7c7b-0f71-0f6e0712042d", // invalid RFC 4122 variant
		"0195f8db7a6f7c7b8f710f6e0712042d",
	}
	for _, value := range invalidIDs {
		if _, err := ParseOperationID(value); err == nil {
			t.Errorf("ParseOperationID(%q) unexpectedly succeeded", value)
		}
	}
}

func TestValidateCreatePreservesProviderOffsetAndCanonicalizesFingerprint(t *testing.T) {
	req := validCreateRequest()
	first, err := ValidateCreate(testOperationID, testScope, testIdentityKey, req)
	if err != nil {
		t.Fatalf("ValidateCreate: %v", err)
	}
	if first.StartProvider != "2026-04-12T08:45:00.000+02:00" {
		t.Fatalf("provider start = %q", first.StartProvider)
	}
	if !strings.HasPrefix(first.AccountIdentity, "hmac-sha256:v1:") || strings.Contains(first.AccountIdentity, "player") {
		t.Fatalf("account identity is not a non-plaintext keyed fingerprint: %q", first.AccountIdentity)
	}

	// Same instants, login case, and a rotated password are the same immutable
	// operation. Provider offset representation is deliberately not the conflict key.
	equivalent := req
	equivalent.Start = "2026-04-12T07:45:00+01:00"
	equivalent.End = "2026-04-12T08:30:00+01:00"
	equivalent.Email = "player@EXAMPLE.COM"
	equivalent.Password = "rotated-secret"
	second, err := ValidateCreate(strings.ToUpper(testOperationID), testScope, testIdentityKey, equivalent)
	if err != nil {
		t.Fatalf("ValidateCreate equivalent: %v", err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatalf("equivalent fingerprints differ:\n%s\n%s", first.Fingerprint(), second.Fingerprint())
	}

	changed := req
	changed.CourtUUID = "another-court"
	third, err := ValidateCreate(testOperationID, testScope, testIdentityKey, changed)
	if err != nil {
		t.Fatalf("ValidateCreate changed: %v", err)
	}
	if first.Fingerprint() == third.Fingerprint() {
		t.Fatal("different court produced the same fingerprint")
	}
}

func TestValidateCreateRiskAuthorizationIsPartOfFingerprint(t *testing.T) {
	base, err := ValidateCreate(testOperationID, testScope, testIdentityKey, validCreateRequest())
	if err != nil {
		t.Fatal(err)
	}

	req := validCreateRequest()
	req.Risk = &RiskAuthorization{
		ReplacesOperationID: testParentID,
		AcceptDuplicateRisk: true,
		Reason:              "midnight slot would otherwise be missed",
	}
	risk, err := ValidateCreate(testOperationID, testScope, testIdentityKey, req)
	if err != nil {
		t.Fatalf("ValidateCreate risk: %v", err)
	}
	if risk.Risk == nil || risk.Risk.ReplacesOperationID != testParentID {
		t.Fatalf("risk authorization = %+v", risk.Risk)
	}
	if base.Fingerprint() == risk.Fingerprint() {
		t.Fatal("risk authorization did not change fingerprint")
	}

	req.Risk.AcceptDuplicateRisk = false
	_, err = ValidateCreate(testOperationID, testScope, testIdentityKey, req)
	assertValidationError(t, err, "risk.accept_duplicate_risk")
}

func TestValidateCreateRejectsInvalidTimesWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		field string
	}{
		{name: "invalid", start: "not-a-time", end: "2026-04-12T09:30:00+02:00", field: "start"},
		{name: "UTC designator", start: "2026-04-12T06:45:00Z", end: "2026-04-12T07:30:00Z", field: "start"},
		{name: "sub-millisecond", start: "2026-04-12T08:45:00.000001+02:00", end: "2026-04-12T09:30:00+02:00", field: "start"},
		{name: "inverted", start: "2026-04-12T09:30:00+02:00", end: "2026-04-12T08:45:00+02:00", field: "end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validCreateRequest()
			req.Start, req.End = tc.start, tc.end
			_, err := ValidateCreate(testOperationID, testScope, testIdentityKey, req)
			assertValidationError(t, err, tc.field)
			if strings.Contains(err.Error(), tc.start) {
				t.Fatal("validation error leaked input value")
			}
		})
	}
}

func TestDeriveProviderAliasIsKeyedAndProviderScoped(t *testing.T) {
	first, err := DeriveProviderAlias(testIdentityKey, "eversports", "provider-user-1")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := DeriveProviderAlias(testIdentityKey, "eversports", "provider-user-1")
	if err != nil {
		t.Fatal(err)
	}
	otherProvider, err := DeriveProviderAlias(testIdentityKey, "other", "provider-user-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != replay || first == otherProvider || strings.Contains(first, "provider-user-1") {
		t.Fatalf("aliases first/replay/other = %q/%q/%q", first, replay, otherProvider)
	}
	if _, err := DeriveProviderAlias([]byte("short"), "eversports", "provider-user-1"); err == nil {
		t.Fatal("short identity key was accepted")
	}
	if _, err := DeriveProviderAlias(testIdentityKey, "eversports", " bad "); err == nil {
		t.Fatal("invalid provider user ID was accepted")
	}
}

func TestValidateResolutionRequestRejectsUnsafeOrUnboundedMetadata(t *testing.T) {
	valid := ResolutionRequest{ExpectedRevision: 3, MatchID: "match-1", Actor: "management", Reason: "verified exact match"}
	if err := ValidateResolutionRequest(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ResolutionRequest){
		func(request *ResolutionRequest) { request.ExpectedRevision = 0 },
		func(request *ResolutionRequest) { request.MatchID = " match-1" },
		func(request *ResolutionRequest) { request.Actor = "management\nadmin" },
		func(request *ResolutionRequest) { request.Reason = strings.Repeat("r", 257) },
	} {
		request := valid
		mutate(&request)
		if err := ValidateResolutionRequest(request); err == nil {
			t.Fatalf("invalid resolution accepted: %+v", request)
		}
	}
}

func TestValidateCreateAccountIdentityIsProviderScoped(t *testing.T) {
	first, err := ValidateCreate(testOperationID, testScope, testIdentityKey, validCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	otherScope := testScope
	otherScope.Provider = "another-provider"
	second, err := ValidateCreate(testOperationID, otherScope, testIdentityKey, validCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	if first.AccountIdentity == second.AccountIdentity {
		t.Fatal("same login at different providers shared an account identity")
	}
}

func TestValidateCreateRejectsControlCharacters(t *testing.T) {
	req := validCreateRequest()
	req.Risk = &RiskAuthorization{
		ReplacesOperationID: testParentID,
		AcceptDuplicateRisk: true,
		Reason:              "unsafe\nreason",
	}
	_, err := ValidateCreate(testOperationID, testScope, testIdentityKey, req)
	assertValidationError(t, err, "risk.reason")
}

func TestValidateCreateRejectsShortIdentityKey(t *testing.T) {
	_, err := ValidateCreate(testOperationID, testScope, []byte("too-short"), validCreateRequest())
	assertValidationError(t, err, "identity_key")
}

func TestValidateCancelFingerprintExcludesPassword(t *testing.T) {
	first, err := ValidateCancel(testOperationID, testScope, testIdentityKey, "match-1", CancelRequest{Email: "USER@example.com", Password: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidateCancel(testOperationID, testScope, testIdentityKey, "match-1", CancelRequest{Email: "user@example.com", Password: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("password rotation changed cancel fingerprint")
	}
}

func TestOperationStatusJSONContainsNoCredentialFields(t *testing.T) {
	status := OperationStatus{
		OperationID: testOperationID,
		Kind:        OperationCreate,
		State:       StateSucceeded,
		Phase:       PhaseFinalization,
		Outcome:     OutcomeSuccess,
		Evidence:    &ProviderEvidence{BookingUUID: "booking-1", MatchID: "match-1"},
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"password", "email", "cookie", "first-secret"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("status JSON contains forbidden value %q: %s", forbidden, text)
		}
	}
}

func assertValidationError(t *testing.T, err error, field string) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if validationErr.Field != field {
		t.Fatalf("validation field = %q, want %q", validationErr.Field, field)
	}
}

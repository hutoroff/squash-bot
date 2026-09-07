package bookingapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxIdentifierLength = 200
	maxServiceKeyLength = 128
	maxEmailLength      = 320
	maxPasswordLength   = 1024
	maxRiskReasonLength = 256
	minIdentityKeyBytes = 32
)

// ValidationError identifies one stable invalid field without echoing its
// potentially secret value.
type ValidationError struct {
	Field string
	Code  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("booking API validation: %s: %s", e.Field, e.Code)
}

// OperationID is a canonical lowercase RFC 9562-variant UUID string. Versions
// 1-8 are accepted so callers can use random or time-ordered UUIDs without changing the
// wire contract.
type OperationID string

func ParseOperationID(value string) (OperationID, error) {
	if value != strings.TrimSpace(value) {
		return "", invalid("operation_id", "invalid_uuid")
	}
	canonical := strings.ToLower(value)
	if len(canonical) != 36 || canonical[8] != '-' || canonical[13] != '-' || canonical[18] != '-' || canonical[23] != '-' {
		return "", invalid("operation_id", "invalid_uuid")
	}

	hexText := strings.NewReplacer("-", "").Replace(canonical)
	decoded, err := hex.DecodeString(hexText)
	if err != nil || len(decoded) != 16 {
		return "", invalid("operation_id", "invalid_uuid")
	}
	allZero := true
	for _, b := range decoded {
		allZero = allZero && b == 0
	}
	version := decoded[6] >> 4
	if allZero || version < 1 || version > 8 || decoded[8]&0xc0 != 0x80 {
		return "", invalid("operation_id", "invalid_uuid")
	}
	return OperationID(canonical), nil
}

type ValidatedCreate struct {
	OperationID     OperationID
	Scope           Scope
	CourtUUID       string
	Start           time.Time
	End             time.Time
	StartProvider   string
	EndProvider     string
	AccountIdentity string
	Risk            *ValidatedRiskAuthorization
}

type ValidatedRiskAuthorization struct {
	ReplacesOperationID OperationID
	Reason              string
}

type ValidatedCancel struct {
	OperationID     OperationID
	Scope           Scope
	MatchID         string
	AccountIdentity string
}

func ValidateCreate(operationID string, scope Scope, identityKey []byte, req CreateRequest) (ValidatedCreate, error) {
	id, err := ParseOperationID(operationID)
	if err != nil {
		return ValidatedCreate{}, err
	}
	if err := validateScope(scope); err != nil {
		return ValidatedCreate{}, err
	}
	if err := validateIdentifier("courtUuid", req.CourtUUID); err != nil {
		return ValidatedCreate{}, err
	}
	account, err := deriveAccountIdentity(identityKey, scope.Provider, req.Email, req.Password)
	if err != nil {
		return ValidatedCreate{}, err
	}
	start, startProvider, err := parseProviderTime("start", req.Start)
	if err != nil {
		return ValidatedCreate{}, err
	}
	end, endProvider, err := parseProviderTime("end", req.End)
	if err != nil {
		return ValidatedCreate{}, err
	}
	if !end.After(start) {
		return ValidatedCreate{}, invalid("end", "must_be_after_start")
	}

	var risk *ValidatedRiskAuthorization
	if req.Risk != nil {
		if !req.Risk.AcceptDuplicateRisk {
			return ValidatedCreate{}, invalid("risk.accept_duplicate_risk", "must_be_true")
		}
		parent, err := ParseOperationID(req.Risk.ReplacesOperationID)
		if err != nil {
			return ValidatedCreate{}, invalid("risk.replaces_operation_id", "invalid_uuid")
		}
		if parent == id {
			return ValidatedCreate{}, invalid("risk.replaces_operation_id", "must_differ_from_operation_id")
		}
		reason := strings.TrimSpace(req.Risk.Reason)
		if reason == "" || len(reason) > maxRiskReasonLength || !safeText(reason) {
			return ValidatedCreate{}, invalid("risk.reason", "invalid_length")
		}
		risk = &ValidatedRiskAuthorization{ReplacesOperationID: parent, Reason: reason}
	}

	return ValidatedCreate{
		OperationID:     id,
		Scope:           scope,
		CourtUUID:       req.CourtUUID,
		Start:           start,
		End:             end,
		StartProvider:   startProvider,
		EndProvider:     endProvider,
		AccountIdentity: account,
		Risk:            risk,
	}, nil
}

func ValidateCancel(operationID string, scope Scope, identityKey []byte, matchID string, req CancelRequest) (ValidatedCancel, error) {
	id, err := ParseOperationID(operationID)
	if err != nil {
		return ValidatedCancel{}, err
	}
	if err := validateScope(scope); err != nil {
		return ValidatedCancel{}, err
	}
	if err := validateIdentifier("match_id", matchID); err != nil {
		return ValidatedCancel{}, err
	}
	account, err := deriveAccountIdentity(identityKey, scope.Provider, req.Email, req.Password)
	if err != nil {
		return ValidatedCancel{}, err
	}
	return ValidatedCancel{OperationID: id, Scope: scope, MatchID: matchID, AccountIdentity: account}, nil
}

// Fingerprint returns the immutable request fingerprint persisted for create
// deduplication. Provider credentials are intentionally excluded.
func (v ValidatedCreate) Fingerprint() string {
	riskParent, riskReason := "", ""
	if v.Risk != nil {
		riskParent = string(v.Risk.ReplacesOperationID)
		riskReason = v.Risk.Reason
	}
	return fingerprint(struct {
		Version   int           `json:"version"`
		Kind      OperationKind `json:"kind"`
		Scope     Scope         `json:"scope"`
		Account   string        `json:"account"`
		CourtUUID string        `json:"court_uuid"`
		Start     string        `json:"start"`
		End       string        `json:"end"`
		Parent    string        `json:"risk_parent"`
		Reason    string        `json:"risk_reason"`
	}{
		Version:   1,
		Kind:      OperationCreate,
		Scope:     v.Scope,
		Account:   v.AccountIdentity,
		CourtUUID: v.CourtUUID,
		Start:     v.Start.UTC().Format(time.RFC3339Nano),
		End:       v.End.UTC().Format(time.RFC3339Nano),
		Parent:    riskParent,
		Reason:    riskReason,
	})
}

// Fingerprint returns the immutable request fingerprint persisted for cancel
// deduplication. Provider credentials are intentionally excluded.
func (v ValidatedCancel) Fingerprint() string {
	return fingerprint(struct {
		Version int           `json:"version"`
		Kind    OperationKind `json:"kind"`
		Scope   Scope         `json:"scope"`
		Account string        `json:"account"`
		MatchID string        `json:"match_id"`
	}{Version: 1, Kind: OperationCancel, Scope: v.Scope, Account: v.AccountIdentity, MatchID: v.MatchID})
}

func validateScope(scope Scope) error {
	fields := []struct {
		name  string
		value string
		max   int
	}{
		{"scope.service_key", scope.ServiceKey, maxServiceKeyLength},
		{"scope.provider", scope.Provider, maxIdentifierLength},
		{"scope.facility_id", scope.FacilityID, maxIdentifierLength},
		{"scope.facility_uuid", scope.FacilityUUID, maxIdentifierLength},
		{"scope.facility_slug", scope.FacilitySlug, maxIdentifierLength},
		{"scope.sport", scope.Sport, maxIdentifierLength},
	}
	for _, field := range fields {
		if field.value == "" || field.value != strings.TrimSpace(field.value) || len(field.value) > field.max || !safeText(field.value) {
			return invalid(field.name, "invalid_length_or_whitespace")
		}
	}
	return nil
}

func validateIdentifier(field, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxIdentifierLength || !safeText(value) {
		return invalid(field, "invalid_length_or_whitespace")
	}
	return nil
}

// DeriveProviderAlias creates a durable keyed fingerprint for the provider
// user learned after login. The raw provider identifier must remain in memory.
func DeriveProviderAlias(identityKey []byte, provider, providerUserID string) (string, error) {
	if len(identityKey) < minIdentityKeyBytes {
		return "", invalid("identity_key", "too_short")
	}
	if err := validateIdentifier("provider_user_id", providerUserID); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, identityKey)
	_, _ = mac.Write([]byte("booking-provider-user:v1\x00" + provider + "\x00" + providerUserID))
	return "hmac-sha256:v1:" + hex.EncodeToString(mac.Sum(nil)), nil
}

func deriveAccountIdentity(identityKey []byte, provider, email, password string) (string, error) {
	if len(identityKey) < minIdentityKeyBytes {
		return "", invalid("identity_key", "too_short")
	}
	email = strings.TrimSpace(email)
	if email == "" || len(email) > maxEmailLength || !safeText(email) {
		return "", invalid("email", "invalid_length")
	}
	if password == "" || len(password) > maxPasswordLength {
		return "", invalid("password", "invalid_length")
	}
	mac := hmac.New(sha256.New, identityKey)
	_, _ = mac.Write([]byte("booking-account:v1\x00" + provider + "\x00" + strings.ToLower(email)))
	return "hmac-sha256:v1:" + hex.EncodeToString(mac.Sum(nil)), nil
}

func safeText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parseProviderTime(field, value string) (time.Time, string, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return time.Time{}, "", invalid(field, "invalid_rfc3339")
	}
	// Booking timestamps must carry an explicit numeric facility-local offset;
	// accepting Z would allow callers to reintroduce the previously prohibited
	// UTC conversion at this boundary.
	if strings.HasSuffix(value, "Z") || strings.HasSuffix(value, "z") {
		return time.Time{}, "", invalid(field, "numeric_offset_required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, "", invalid(field, "invalid_rfc3339")
	}
	if parsed.Nanosecond()%int(time.Millisecond) != 0 {
		return time.Time{}, "", invalid(field, "unsupported_sub_millisecond_precision")
	}
	// Keep the caller's offset for the provider while normalizing supported
	// precision. Conflict identity is based on the UTC instant in Fingerprint.
	return parsed, parsed.Format("2006-01-02T15:04:05.000Z07:00"), nil
}

func fingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("booking API fingerprint invariant: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func invalid(field, code string) error {
	return &ValidationError{Field: field, Code: code}
}

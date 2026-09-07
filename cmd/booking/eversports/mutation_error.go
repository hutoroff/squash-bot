package eversports

import (
	"errors"
	"fmt"
)

// MutationPhase identifies the last provider phase attempted by a mutating
// operation. It is intentionally independent of HTTP status codes.
type MutationPhase string

const (
	MutationPhaseLogin           MutationPhase = "login"
	MutationPhaseReserve         MutationPhase = "reserve"
	MutationPhasePayment         MutationPhase = "payment"
	MutationPhaseMatchAttachment MutationPhase = "match_attachment"
	MutationPhaseCancellation    MutationPhase = "cancellation"
)

// MutationEffect is the strongest statement the adapter can make about the
// provider-side effect after an error.
type MutationEffect string

const (
	MutationNoEffect MutationEffect = "no_effect"
	MutationUnknown  MutationEffect = "unknown"
)

type MutationCode string

const (
	MutationCodeRequestBuildFailed    MutationCode = "request_build_failed"
	MutationCodeTransportError        MutationCode = "transport_error"
	MutationCodeResponseReadFailed    MutationCode = "response_read_failed"
	MutationCodeHTTPError             MutationCode = "http_error"
	MutationCodeInvalidResponse       MutationCode = "invalid_response"
	MutationCodeProviderError         MutationCode = "provider_error"
	MutationCodeProviderRejected      MutationCode = "provider_rejected"
	MutationCodeAuthenticationExpired MutationCode = "authentication_expired"
	MutationCodeAuthenticationFailed  MutationCode = "authentication_failed"
	MutationCodeCredentialRejected    MutationCode = "credential_rejected"
)

// AuthenticationError distinguishes a positively identified credential
// rejection from login transport/protocol failures. Rejected is set only for
// an explicit provider credential-login rejection.
type AuthenticationError struct {
	Rejected bool
	Err      error
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("eversports authentication: %v", e.Err)
}
func (e *AuthenticationError) Unwrap() error { return e.Err }

// MutationError preserves the provider phase and effect certainty needed by
// the durable coordinator. RetrySafe is true only for an operation-specific,
// positively identified no-effect authentication boundary already supported by
// the adapter; it is not permission for generic transport retries.
type MutationError struct {
	Phase     MutationPhase
	Effect    MutationEffect
	Code      MutationCode
	RetrySafe bool
	Err       error
}

func (e *MutationError) Error() string {
	return fmt.Sprintf("eversports %s: %s: %v", e.Phase, e.Code, e.Err)
}

func (e *MutationError) Unwrap() error { return e.Err }

func mutationFailure(phase MutationPhase, effect MutationEffect, code MutationCode, retrySafe bool, err error) error {
	return &MutationError{Phase: phase, Effect: effect, Code: code, RetrySafe: retrySafe, Err: err}
}

func loginMutationFailure(err error) error {
	code := MutationCodeAuthenticationFailed
	var authenticationErr *AuthenticationError
	if errors.As(err, &authenticationErr) && authenticationErr.Rejected {
		code = MutationCodeCredentialRejected
	}
	return mutationFailure(MutationPhaseLogin, MutationNoEffect, code, false, err)
}

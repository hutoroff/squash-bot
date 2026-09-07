package eversports

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCreateBookingUnclassifiedLoginRejectionDoesNotClaimBadCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+graphqlEndpoint, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"credentialLogin":{"__typename":"ExpectedErrors","errors":[{"message":"invalid credentials"}]}}}`)
	})
	client := newTestClient(t, mux)

	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.UTC)
	_, err := client.CreateBooking(context.Background(), "facility", "court", "sport", start, start.Add(45*time.Minute))
	mutationErr := requireMutationError(t, err, MutationPhaseLogin, MutationNoEffect)
	if mutationErr.Code != "authentication_failed" || mutationErr.RetrySafe {
		t.Fatalf("unclassified login rejection = code %q retry_safe %t", mutationErr.Code, mutationErr.RetrySafe)
	}
}

func TestExplicitCredentialRejectionMapsToStableMutationCode(t *testing.T) {
	err := loginMutationFailure(&AuthenticationError{Rejected: true, Err: errors.New("captured provider credential rejection")})
	mutationErr := requireMutationError(t, err, MutationPhaseLogin, MutationNoEffect)
	if mutationErr.Code != "credential_rejected" {
		t.Fatalf("credential rejection code = %q", mutationErr.Code)
	}
}

func TestCreateBookingGatewayTimeoutIsTypedUnknownAndNotRetryable(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	mux.HandleFunc("POST "+courtBookingEndpoint, func(w http.ResponseWriter, _ *http.Request) {
		ctr.step1.Add(1)
		http.Error(w, "forwarded request timed out", http.StatusGatewayTimeout)
	})
	client := newTestClient(t, mux)

	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.FixedZone("CEST", 2*60*60))
	_, err := client.CreateBooking(context.Background(), "facility", "court", "sport", start, start.Add(45*time.Minute))
	mutationErr := requireMutationError(t, err, MutationPhaseReserve, MutationUnknown)
	if mutationErr.RetrySafe {
		t.Fatal("ambiguous reserve timeout must not be retry-safe")
	}
	if got := ctr.step1.Load(); got != 1 {
		t.Fatalf("reserve calls = %d, want 1", got)
	}
}

func TestCreateBookingRejectionShapedResponseRemainsUnknown(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	mux.HandleFunc("POST "+courtBookingEndpoint, func(w http.ResponseWriter, _ *http.Request) {
		ctr.step1.Add(1)
		fmt.Fprint(w, `{"success":false,"status":"REJECTED"}`)
	})
	client := newTestClient(t, mux)

	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.UTC)
	_, err := client.CreateBooking(context.Background(), "facility", "court", "sport", start, start.Add(45*time.Minute))
	mutationErr := requireMutationError(t, err, MutationPhaseReserve, MutationUnknown)
	if mutationErr.Code != "provider_rejected" {
		t.Fatalf("rejection-shaped response code = %q", mutationErr.Code)
	}
}

func TestCreateBookingMissingReservationIDsIsTypedUnknown(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	mux.HandleFunc("POST "+courtBookingEndpoint, func(w http.ResponseWriter, _ *http.Request) {
		ctr.step1.Add(1)
		fmt.Fprint(w, `{"success":true,"status":"CONFIRMED","bookingUuid":"","bookingId":0,"payment":{"id":0}}`)
	})
	client := newTestClient(t, mux)

	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.UTC)
	_, err := client.CreateBooking(context.Background(), "facility", "court", "sport", start, start.Add(45*time.Minute))
	requireMutationError(t, err, MutationPhaseReserve, MutationUnknown)
}

func TestCreateBookingMalformedPaymentAcknowledgmentIsTypedUnknown(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	mux.HandleFunc("POST "+courtBookingEndpoint, func(w http.ResponseWriter, _ *http.Request) {
		ctr.step1.Add(1)
		fmt.Fprint(w, `{"bookingUuid":"booking-1","bookingId":42,"payment":{"id":7},"success":true,"status":"CONFIRMED"}`)
	})
	mux.HandleFunc("POST /checkout/api/payment/{id}/pay-offline", func(w http.ResponseWriter, _ *http.Request) {
		ctr.payOffline.Add(1)
		fmt.Fprint(w, `not-json`)
	})
	client := newTestClient(t, mux)

	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.UTC)
	_, err := client.CreateBooking(context.Background(), "facility", "court", "sport", start, start.Add(45*time.Minute))
	mutationErr := requireMutationError(t, err, MutationPhasePayment, MutationUnknown)
	if mutationErr.RetrySafe || errors.Is(err, errUnauthorized) {
		t.Fatal("malformed payment acknowledgment must not restart checkout")
	}
}

func TestCancelMatchBusinessRejectionIsTypedNoEffect(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, map[string]gqlHandler{
		"CancelMatch": func(_ int, w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":{"cancelMatch":{"__typename":"ExpectedErrors","errors":[{"message":"too late"}]}}}`)
		},
	})
	client := newTestClient(t, mux)

	_, err := client.CancelMatch(context.Background(), "match-1")
	mutationErr := requireMutationError(t, err, MutationPhaseCancellation, MutationNoEffect)
	if mutationErr.RetrySafe {
		t.Fatal("business rejection must not be retry-safe")
	}
}

func TestCancelMatchUnconfirmedResponseIsTypedUnknown(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, map[string]gqlHandler{
		"CancelMatch": func(_ int, w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":{"cancelMatch":{"__typename":"BallsportMatch","id":"different-match","state":"ACTIVE"}}}`)
		},
	})
	client := newTestClient(t, mux)

	_, err := client.CancelMatch(context.Background(), "match-1")
	mutationErr := requireMutationError(t, err, MutationPhaseCancellation, MutationUnknown)
	if mutationErr.RetrySafe {
		t.Fatal("unconfirmed cancellation must not be retry-safe")
	}
	if got := ctr.cancels.Load(); got != 1 {
		t.Fatalf("cancel calls = %d, want 1", got)
	}
}

func requireMutationError(t *testing.T, err error, phase MutationPhase, effect MutationEffect) *MutationError {
	t.Helper()
	if err == nil {
		t.Fatal("expected mutation error")
	}
	var mutationErr *MutationError
	if !errors.As(err, &mutationErr) {
		t.Fatalf("error = %v, want *MutationError", err)
	}
	if mutationErr.Phase != phase || mutationErr.Effect != effect {
		t.Fatalf("mutation error = phase %q effect %q, want %q/%q", mutationErr.Phase, mutationErr.Effect, phase, effect)
	}
	return mutationErr
}

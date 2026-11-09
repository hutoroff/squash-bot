package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ─── Single match (confirmed) ─────────────────────────────────────────────────

// matchQuery is the GraphQL query for a single booking, confirmed from a live
// browser DevTools capture.
const matchQuery = `query Match($matchId: ID!) {
  match(matchId: $matchId) {
    ... on BallsportMatch {
      id
      start
      end
      state
      sport { id name slug __typename }
      venue {
        id address name shortId slug
        location { latitude longitude __typename }
        __typename
      }
      court { name area surface __typename }
      price { value currency __typename }
      __typename
    }
    __typename
  }
}`

// rawMatch is the GraphQL response shape for the Match query.
// Field names are confirmed from a live API capture.
type rawMatch struct {
	ID    string `json:"id"`
	Start string `json:"start"` // RFC3339 / ISO-8601
	End   string `json:"end"`
	State string `json:"state"`
	Sport struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"sport"`
	Venue struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
		ShortID string `json:"shortId"`
		Slug    string `json:"slug"`
	} `json:"venue"`
	Court struct {
		Name    string `json:"name"`
		Area    string `json:"area"`
		Surface string `json:"surface"`
	} `json:"court"`
	Price struct {
		Value    int    `json:"value"`
		Currency string `json:"currency"`
	} `json:"price"`
}

func (r rawMatch) toBooking() (Booking, error) {
	start, err := parseTime(r.Start)
	if err != nil {
		return Booking{}, err
	}
	end, err := parseTime(r.End)
	if err != nil {
		return Booking{}, err
	}
	b := Booking{
		ID:    r.ID,
		Start: start,
		End:   end,
		State: r.State,
	}
	b.Sport.Name = r.Sport.Name
	b.Sport.Slug = r.Sport.Slug
	b.Venue.ID = r.Venue.ID
	b.Venue.Name = r.Venue.Name
	b.Venue.Address = r.Venue.Address
	b.Venue.ShortID = r.Venue.ShortID
	b.Venue.Slug = r.Venue.Slug
	b.Court.Name = r.Court.Name
	b.Court.Area = r.Court.Area
	b.Court.Surface = r.Court.Surface
	b.Price.Value = r.Price.Value
	b.Price.Currency = r.Price.Currency
	return b, nil
}

// gqlMatchResponse is the GraphQL response envelope for the Match query.
type gqlMatchResponse struct {
	Data struct {
		Match rawMatch `json:"match"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// GetMatchByID fetches the details of a single booking by its UUID.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetMatchByID(ctx context.Context, matchID string) (*Booking, error) {
	do := func() (*Booking, error) {
		payload := gqlRequest{
			OperationName: "Match",
			Variables:     map[string]any{"matchId": matchID},
			Query:         matchQuery,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal Match request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, c.baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: Match request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := readProviderResponse(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read Match response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: Match HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes))
		}
		if err := htmlAuthError("Match", respBytes); err != nil {
			return nil, err
		}

		var gqlResp gqlMatchResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode Match response: %w (body: %s)", err, bodySnippet(respBytes))
		}
		if len(gqlResp.Errors) > 0 {
			return nil, gqlTopLevelError("Match", gqlResp.Errors[0].Message)
		}
		b, err := gqlResp.Data.Match.toBooking()
		if err != nil {
			return nil, fmt.Errorf("eversports: parse Match times: %w", err)
		}
		return &b, nil
	}

	return withAuth(ctx, c, do)
}

// ─── CancelMatch ──────────────────────────────────────────────────────────────

// cancelMatchMutation is the GraphQL mutation captured from a live browser
// DevTools request to cancel a court booking.
const cancelMatchMutation = `mutation CancelMatch($matchId: ID!, $origin: Origin!) {
  cancelMatch(matchId: $matchId, origin: $origin) {
    ... on BallsportMatch {
      id
      state
      relativeLink
      __typename
    }
    ... on ExpectedErrors {
      errors {
        id
        message
        path
        __typename
      }
      __typename
    }
    __typename
  }
}`

type cancelMatchPayload struct {
	Typename string `json:"__typename"`
	// BallsportMatch fields
	ID           string `json:"id"`
	State        string `json:"state"`
	RelativeLink string `json:"relativeLink"`
	// ExpectedErrors fields
	Errors []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Path    string `json:"path"`
	} `json:"errors"`
}

// gqlCancelMatchResponse is the GraphQL response envelope for the CancelMatch mutation.
type gqlCancelMatchResponse struct {
	Data struct {
		CancelMatch cancelMatchPayload `json:"cancelMatch"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// CancelMatchOnce performs exactly one cancellation mutation. It assumes a
// session was established and never reauthenticates or retries.
func (c *Client) CancelMatchOnce(ctx context.Context, matchID string) (*CancellationResult, error) {
	payload := gqlRequest{
		OperationName: "CancelMatch",
		Variables: map[string]any{
			"matchId": matchID,
			"origin":  "ORIGIN_MARKETPLACE",
		},
		Query: cancelMatchMutation,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationNoEffect, MutationCodeRequestBuildFailed, false,
			fmt.Errorf("marshal CancelMatch request: %w", err))
	}
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, c.baseURL+graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeTransportError, false,
			fmt.Errorf("CancelMatch request: %w", err))
	}
	defer resp.Body.Close()

	respBytes, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeResponseReadFailed, false,
			fmt.Errorf("read CancelMatch response: %w", err))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, mutationFailure(MutationPhaseCancellation, MutationNoEffect, MutationCodeAuthenticationExpired, true, errUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeHTTPError, false,
			fmt.Errorf("CancelMatch HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes)))
	}
	if err := htmlAuthError("CancelMatch", respBytes); err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationNoEffect, MutationCodeAuthenticationExpired, true, err)
	}

	var gqlResp gqlCancelMatchResponse
	if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeInvalidResponse, false,
			fmt.Errorf("decode CancelMatch response: %w (body: %s)", err, bodySnippet(respBytes)))
	}
	if len(gqlResp.Errors) > 0 {
		topErr := gqlTopLevelError("CancelMatch", gqlResp.Errors[0].Message)
		if errors.Is(topErr, errUnauthorized) {
			return nil, mutationFailure(MutationPhaseCancellation, MutationNoEffect, MutationCodeAuthenticationExpired, true, topErr)
		}
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeProviderError, false, topErr)
	}
	cm := gqlResp.Data.CancelMatch
	if len(cm.Errors) > 0 {
		return nil, mutationFailure(MutationPhaseCancellation, MutationNoEffect, MutationCodeProviderRejected, false,
			fmt.Errorf("CancelMatch error: %s", cm.Errors[0].Message))
	}
	if err := validateCancellationResponse(cm, matchID); err != nil {
		return nil, mutationFailure(MutationPhaseCancellation, MutationUnknown, MutationCodeInvalidResponse, false, err)
	}
	c.logger.Info("eversports match cancelled", "matchId", matchID, "state", cm.State)
	return &CancellationResult{ID: cm.ID, State: cm.State, RelativeLink: cm.RelativeLink}, nil
}

// CancelMatch preserves the legacy one-auth-retry behavior. Durable v2
// orchestration journals each attempt and calls CancelMatchOnce directly.
func (c *Client) CancelMatch(ctx context.Context, matchID string) (*CancellationResult, error) {
	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, loginMutationFailure(err)
	}
	result, err := c.CancelMatchOnce(ctx, matchID)
	var mutationErr *MutationError
	if errors.As(err, &mutationErr) && mutationErr.RetrySafe && errors.Is(err, errUnauthorized) {
		if loginErr := c.Reauthenticate(ctx); loginErr != nil {
			return nil, loginMutationFailure(loginErr)
		}
		return c.CancelMatchOnce(ctx, matchID)
	}
	return result, err
}

func validateCancellationResponse(response cancelMatchPayload, requestedMatchID string) error {
	if response.Typename != "BallsportMatch" {
		return fmt.Errorf("CancelMatch returned unexpected type %q", response.Typename)
	}
	if response.ID == "" || response.ID != requestedMatchID {
		return fmt.Errorf("CancelMatch returned a mismatched or empty match identifier")
	}
	if response.State != "CANCELLED" {
		return fmt.Errorf("CancelMatch returned unconfirmed state %q", response.State)
	}
	return nil
}

package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: Match request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read Match response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: Match HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var gqlResp gqlMatchResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode Match response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: Match graphql error: %s", gqlResp.Errors[0].Message)
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

// gqlCancelMatchResponse is the GraphQL response envelope for the CancelMatch mutation.
type gqlCancelMatchResponse struct {
	Data struct {
		CancelMatch struct {
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
		} `json:"cancelMatch"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// CancelMatch cancels a booking by its UUID.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) CancelMatch(ctx context.Context, matchID string) (*CancellationResult, error) {
	do := func() (*CancellationResult, error) {
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
			return nil, fmt.Errorf("eversports: marshal CancelMatch request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: CancelMatch request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read CancelMatch response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: CancelMatch HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var gqlResp gqlCancelMatchResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode CancelMatch response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: CancelMatch graphql error: %s", gqlResp.Errors[0].Message)
		}
		cm := gqlResp.Data.CancelMatch
		if len(cm.Errors) > 0 {
			return nil, fmt.Errorf("eversports: CancelMatch error: %s", cm.Errors[0].Message)
		}
		c.logger.Info("eversports match cancelled", "matchId", matchID, "state", cm.State)
		return &CancellationResult{
			ID:           cm.ID,
			State:        cm.State,
			RelativeLink: cm.RelativeLink,
		}, nil
	}

	return withAuth(ctx, c, do)
}

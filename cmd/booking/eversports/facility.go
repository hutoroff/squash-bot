package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ─── Facility (venue profile) ─────────────────────────────────────────────────

// venueProfileQuery fetches only the public-facing venue fields we expose.
// Excluded: meta, about, amenities, cheapestPrice, cheapestTrialProductPrice,
// faqs, images, location, logo, offerings, ratings, reviews, specialPriceTypes, trainers.
const venueProfileQuery = `query VenueProfileVenueContext($slug: String!) {
  venueContext(slug: $slug) {
    venue {
      id
      slug
      name
      rating
      reviewCount
      address
      hideAddress
      tags { name }
      contact { email facebook instagram website telephone }
      sports { id name slug }
      city { id slug }
      company {
        venues {
          id name slug
          location { city zip country }
        }
      }
    }
  }
}`

// gqlFacilityResponse is the GraphQL response envelope for VenueProfileVenueContext.
type gqlFacilityResponse struct {
	Data struct {
		VenueContext struct {
			Venue struct {
				ID          string  `json:"id"`
				Slug        string  `json:"slug"`
				Name        string  `json:"name"`
				Rating      float64 `json:"rating"`
				ReviewCount int     `json:"reviewCount"`
				Address     string  `json:"address"`
				HideAddress bool    `json:"hideAddress"`
				Tags        []struct {
					Name string `json:"name"`
				} `json:"tags"`
				Contact struct {
					Email     string `json:"email"`
					Facebook  string `json:"facebook"`
					Instagram string `json:"instagram"`
					Website   string `json:"website"`
					Telephone string `json:"telephone"`
				} `json:"contact"`
				Sports []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Slug string `json:"slug"`
				} `json:"sports"`
				City struct {
					ID   string `json:"id"`
					Slug string `json:"slug"`
				} `json:"city"`
				Company struct {
					Venues []struct {
						ID       string `json:"id"`
						Name     string `json:"name"`
						Slug     string `json:"slug"`
						Location struct {
							City    string `json:"city"`
							Zip     string `json:"zip"`
							Country string `json:"country"`
						} `json:"location"`
					} `json:"venues"`
				} `json:"company"`
			} `json:"venue"`
		} `json:"venueContext"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// GetFacility fetches the venue profile for the given facility slug.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetFacility(ctx context.Context, slug string) (*Facility, error) {
	do := func() (*Facility, error) {
		payload := gqlRequest{
			OperationName: "VenueProfileVenueContext",
			Variables:     map[string]any{"slug": slug},
			Query:         venueProfileQuery,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal facility request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: facility request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read facility response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: facility HTTP %d: %s", resp.StatusCode, string(respBytes))
		}
		var gqlResp gqlFacilityResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode facility response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: facility GraphQL error: %s", gqlResp.Errors[0].Message)
		}

		v := gqlResp.Data.VenueContext.Venue
		if v.ID == "" {
			return nil, fmt.Errorf("eversports: facility slug %q: %w", slug, ErrNotFound)
		}
		f := &Facility{
			ID:          v.ID,
			Slug:        v.Slug,
			Name:        v.Name,
			Rating:      v.Rating,
			ReviewCount: v.ReviewCount,
			Address:     v.Address,
			HideAddress: v.HideAddress,
			Contact: FacilityContact{
				Email:     v.Contact.Email,
				Facebook:  v.Contact.Facebook,
				Instagram: v.Contact.Instagram,
				Website:   v.Contact.Website,
				Telephone: v.Contact.Telephone,
			},
			City: FacilityCity{ID: v.City.ID, Slug: v.City.Slug},
		}
		for _, t := range v.Tags {
			f.Tags = append(f.Tags, FacilityTag{Name: t.Name})
		}
		for _, s := range v.Sports {
			f.Sports = append(f.Sports, FacilitySport{ID: s.ID, Name: s.Name, Slug: s.Slug})
		}
		for _, cv := range v.Company.Venues {
			f.Company.Venues = append(f.Company.Venues, FacilityVenueRef{
				ID:   cv.ID,
				Name: cv.Name,
				Slug: cv.Slug,
				Location: FacilityVenueLocation{
					City:    cv.Location.City,
					Zip:     cv.Location.Zip,
					Country: cv.Location.Country,
				},
			})
		}

		c.logger.Info("eversports facility fetched", "slug", slug, "name", f.Name)
		return f, nil
	}

	return withAuth(ctx, c, do)
}

package eversports

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestAttachMatchResponseHandling(t *testing.T) {
	tests := []struct {
		name          string
		attach        func(w http.ResponseWriter, r *http.Request)
		wantMatchID   string
		wantCode      MutationCode
		wantFollowups int64
		wantPosts     int64
	}{
		{
			name: "200 json",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `{"matchId":"match-200"}`)
			},
			wantMatchID: "match-200", wantPosts: 1,
		},
		{
			name: "201 json",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"matchId":"match-201"}`)
			},
			wantMatchID: "match-201", wantPosts: 1,
		},
		{
			name: "302 same origin read",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/match-result?token=secret")
				w.WriteHeader(http.StatusFound)
			},
			wantMatchID: "match-redirected", wantFollowups: 1, wantPosts: 1,
		},
		{
			name: "303 same origin read",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/match-result")
				w.WriteHeader(http.StatusSeeOther)
			},
			wantMatchID: "match-redirected", wantFollowups: 1, wantPosts: 1,
		},
		{
			name: "307 is never replayed",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/match-result")
				w.WriteHeader(http.StatusTemporaryRedirect)
			},
			wantCode: MutationCodeHTTPError, wantPosts: 1,
		},
		{
			name: "308 is never replayed",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/match-result")
				w.WriteHeader(http.StatusPermanentRedirect)
			},
			wantCode: MutationCodeHTTPError, wantPosts: 1,
		},
		{
			name: "cross origin redirect is not followed",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://other.example/match-result")
				w.WriteHeader(http.StatusFound)
			},
			wantCode: MutationCodeHTTPError, wantPosts: 1,
		},
		{
			name: "server error",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			wantCode: MutationCodeHTTPError, wantPosts: 1,
		},
		{
			name: "html body",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<!DOCTYPE html><html></html>`)
			},
			wantCode: MutationCodeInvalidResponse, wantPosts: 1,
		},
		{
			name: "missing match id",
			attach: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"ok":true}`)
			},
			wantCode: MutationCodeInvalidResponse, wantPosts: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var posts, followups, redirectedPosts atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("POST "+createFromBookingEndpoint, func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				tt.attach(w, r)
			})
			mux.HandleFunc("GET /match-result", func(w http.ResponseWriter, _ *http.Request) {
				followups.Add(1)
				fmt.Fprint(w, `{"matchId":"match-redirected"}`)
			})
			mux.HandleFunc("POST /match-result", func(http.ResponseWriter, *http.Request) {
				redirectedPosts.Add(1)
			})
			c := newTestClient(t, mux)

			matchID, err := c.AttachMatch(context.Background(), "booking-uuid")
			if tt.wantCode == "" {
				if err != nil || matchID != tt.wantMatchID {
					t.Fatalf("AttachMatch = %q, %v; want %q", matchID, err, tt.wantMatchID)
				}
			} else {
				var mutationErr *MutationError
				if !errors.As(err, &mutationErr) || mutationErr.Code != tt.wantCode ||
					mutationErr.Phase != MutationPhaseMatchAttachment || mutationErr.Effect != MutationUnknown || mutationErr.RetrySafe {
					t.Fatalf("AttachMatch error = %#v, want code %s", err, tt.wantCode)
				}
			}
			if posts.Load() != tt.wantPosts || followups.Load() != tt.wantFollowups || redirectedPosts.Load() != 0 {
				t.Fatalf("posts/followups/redirectedPosts = %d/%d/%d, want %d/%d/0",
					posts.Load(), followups.Load(), redirectedPosts.Load(), tt.wantPosts, tt.wantFollowups)
			}
		})
	}
}

package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type fakeRiskDecision struct {
	calls                       int
	actor, revision, credential int64
	reason                      string
}

func (f *fakeRiskDecision) AuthorizeAndDispatch(_ context.Context, parent bookingapi.OperationID, revision, credential, actor int64, reason string) (*models.ManagementBookingOperation, bool, error) {
	f.calls++
	f.actor = actor
	f.revision = revision
	f.credential = credential
	f.reason = reason
	return &models.ManagementBookingOperation{OperationID: "bbbbbbbb-bbbb-7bbb-8bbb-bbbbbbbbbbbb", ParentOperationID: parent, Kind: bookingapi.OperationCreate}, true, nil
}

func TestRiskAuthorizationRequiresBearerSignedOwnerAndExplicitAcknowledgement(t *testing.T) {
	secret := strings.Repeat("s", 32)
	mgmtSecret := strings.Repeat("m", 32)
	valid := signedRiskTestSession(secret, fmt.Sprintf(`{"uid":42,"exp":%d}`, time.Now().Add(time.Hour).Unix()))
	nonowner := signedRiskTestSession(secret, fmt.Sprintf(`{"uid":7,"exp":%d}`, time.Now().Add(time.Hour).Unix()))
	h := &Handler{riskSessionSecret: secret, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), userRepo: &fakeUserRepo{byID: map[int64]*models.User{
		42: {ID: 42, IsServerOwner: true}, 7: {ID: 7, IsServerOwner: false},
	}}}
	f := &fakeRiskDecision{}
	h.riskBooking = f
	srv := NewServer(":0", h, mgmtSecret, strings.Repeat("t", 32))
	url := "/api/v1/booking/operations/aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa/authorize-risk"
	call := func(session, bearer, body, actor string) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		r.Header.Set("Authorization", bearer)
		r.Header.Set("X-Web-Session", session)
		r.Header.Set("X-Caller-User-Id", actor)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"expected_revision":2,"credential_id":9,"reason":"accept possible duplicate","accept_duplicate_risk":true}`
	for _, tc := range []struct {
		name, session, bearer, body, actor string
		want                               int
	}{
		{"bearer missing", valid, "", body, "42", 401},
		{"session missing", "", "Bearer " + mgmtSecret, body, "42", 401},
		{"other user", nonowner, "Bearer " + mgmtSecret, body, "42", 403},
		{"acknowledgement absent", valid, "Bearer " + mgmtSecret, `{"expected_revision":2,"credential_id":9,"reason":"reason"}`, "42", 400},
		{"body actor spoof", valid, "Bearer " + mgmtSecret, `{"expected_revision":2,"credential_id":9,"reason":"reason","accept_duplicate_risk":true,"actor_user_id":7}`, "42", 400},
		{"owner", valid, "Bearer " + mgmtSecret, body, "7", 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := call(tc.session, tc.bearer, tc.body, tc.actor); got != tc.want {
				t.Fatalf("status=%d want=%d", got, tc.want)
			}
		})
	}
	if f.calls != 1 || f.actor != 42 || f.revision != 2 || f.credential != 9 || f.reason != "accept possible duplicate" {
		t.Fatalf("risk decision=%+v", f)
	}
	h.riskBooking = nil
	if got := call(valid, "Bearer "+mgmtSecret, body, "42"); got != http.StatusServiceUnavailable || f.calls != 1 {
		t.Fatalf("default-disabled route status=%d calls=%d", got, f.calls)
	}
}

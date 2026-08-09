package webserver

import (
	"net/http"
	"testing"
)

// RegisterRoutes panics on conflicting ServeMux patterns; without this the
// first sign of a bad route would be a crash at service start.
func TestRegisterRoutes_NoPatternConflicts(t *testing.T) {
	auth := testAuthHandler(t, nil)
	h := NewHandler(nil, "test", nil, auth,
		NewGamesHandler(auth, "http://mgmt", "s"),
		NewAuditHandler(auth, "http://mgmt", "s"),
		NewGroupsHandler(auth, "http://mgmt", "s"),
		NewVenuesHandler(auth, "http://mgmt", "s"),
		NewPrefsHandler(auth, "http://mgmt", "s"),
	)
	h.RegisterRoutes(http.NewServeMux())
}

// Package session verifies the web transport's signed canonical identity at
// management-only high-risk boundaries. It does not grant an owner role.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid web session")

// VerifyUserID requires the same HS256 session issued by the web login flow.
// Internal API bearer authentication or an unsigned actor header alone never
// grants this identity; management still checks server-owner status in its DB.
func VerifyUserID(secret, token string) (int64, error) {
	if len(secret) < 32 || len(token) == 0 || len(token) > 4096 {
		return 0, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, ErrInvalid
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalid
	}
	var metadata struct{ Alg, Typ string }
	if json.Unmarshal(header, &metadata) != nil || metadata.Alg != "HS256" || metadata.Typ != "JWT" {
		return 0, ErrInvalid
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(signature, mac.Sum(nil)) != 1 {
		return 0, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, ErrInvalid
	}
	var claims struct {
		UserID int64 `json:"uid"`
		Exp    int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.UserID <= 0 || claims.Exp <= time.Now().Unix() {
		return 0, ErrInvalid
	}
	return claims.UserID, nil
}

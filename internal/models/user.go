package models

import "time"

// IdentityProviderTelegram is the only identity provider supported today.
const IdentityProviderTelegram = "telegram"

// User is the canonical, provider-agnostic identity. A Telegram user, a
// future Strava user, and a future email-only user are all a User with one
// or more UserIdentity rows attached.
type User struct {
	ID            int64     `json:"user_id"`
	DisplayName   string    `json:"display_name"`
	IsServerOwner bool      `json:"is_server_owner"`
	DMLanguage    string    `json:"dm_language"`
	ResultsOptOut bool      `json:"results_opt_out"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserIdentity links a User to one external identity provider account.
type UserIdentity struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Provider   string    `json:"provider"`
	ExternalID string    `json:"external_id"`
	Username   *string   `json:"username,omitempty"`
	FirstName  *string   `json:"first_name,omitempty"`
	LastName   *string   `json:"last_name,omitempty"`
	PhotoURL   *string   `json:"photo_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

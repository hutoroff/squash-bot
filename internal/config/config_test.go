package config

import "testing"

func strOfLen(n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = 'a'
	}
	return string(s)
}

func TestTelegramConfig_Validate(t *testing.T) {
	longSecret := strOfLen(minSecretLength)
	longWebhookSecret := strOfLen(minWebhookSecretLength)

	tests := []struct {
		name    string
		cfg     TelegramConfig
		wantErr bool
	}{
		{
			name:    "polling mode, strong secret",
			cfg:     TelegramConfig{InternalAPISecret: longSecret},
			wantErr: false,
		},
		{
			name:    "polling mode, weak internal secret",
			cfg:     TelegramConfig{InternalAPISecret: "short"},
			wantErr: true,
		},
		{
			name:    "webhook mode without a webhook secret",
			cfg:     TelegramConfig{InternalAPISecret: longSecret, WebhookURL: "https://example.com/hook"},
			wantErr: true,
		},
		{
			name: "webhook mode with a too-short webhook secret",
			cfg: TelegramConfig{
				InternalAPISecret: longSecret,
				WebhookURL:        "https://example.com/hook",
				WebhookSecret:     "short",
			},
			wantErr: true,
		},
		{
			name: "webhook mode with a valid webhook secret",
			cfg: TelegramConfig{
				InternalAPISecret: longSecret,
				WebhookURL:        "https://example.com/hook",
				WebhookSecret:     longWebhookSecret,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManagementConfig_Validate(t *testing.T) {
	if err := (&ManagementConfig{InternalAPISecret: strOfLen(minSecretLength)}).Validate(); err != nil {
		t.Errorf("expected no error for a %d-char secret, got %v", minSecretLength, err)
	}
	if err := (&ManagementConfig{InternalAPISecret: "short"}).Validate(); err == nil {
		t.Error("expected error for a short INTERNAL_API_SECRET")
	}
}

func TestBookingConfig_Validate(t *testing.T) {
	if err := (&BookingConfig{InternalAPISecret: strOfLen(minSecretLength)}).Validate(); err != nil {
		t.Errorf("expected no error for a %d-char secret, got %v", minSecretLength, err)
	}
	if err := (&BookingConfig{InternalAPISecret: "short"}).Validate(); err == nil {
		t.Error("expected error for a short INTERNAL_API_SECRET")
	}
}

func TestWebConfig_Validate(t *testing.T) {
	longSecret := strOfLen(minSecretLength)

	tests := []struct {
		name    string
		cfg     WebConfig
		wantErr bool
	}{
		{
			name:    "both secrets strong",
			cfg:     WebConfig{InternalAPISecret: longSecret, JWTSecret: longSecret},
			wantErr: false,
		},
		{
			name:    "weak internal API secret",
			cfg:     WebConfig{InternalAPISecret: "short", JWTSecret: longSecret},
			wantErr: true,
		},
		{
			name:    "weak JWT secret",
			cfg:     WebConfig{InternalAPISecret: longSecret, JWTSecret: "short"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

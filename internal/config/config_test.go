package config

import (
	"testing"
	"time"
)

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
			cfg:     TelegramConfig{InternalAPISecret: longSecret, ManagementServiceURL: "http://management:8080", ServerPort: "8083", AdminServerPort: "9090", MonitoringToken: strOfLen(minSecretLength + 1), ServiceKey: "telegram"},
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
				InternalAPISecret:    longSecret,
				WebhookURL:           "https://example.com/hook",
				WebhookSecret:        longWebhookSecret,
				ManagementServiceURL: "http://management:8080",
				ServerPort:           "8083",
				AdminServerPort:      "9090",
				MonitoringToken:      strOfLen(minSecretLength + 1),
				ServiceKey:           "telegram",
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
	registry := TelegramConfig{InternalAPISecret: longSecret, ServerPort: "8083", AdminServerPort: "9090", MonitoringToken: strOfLen(minSecretLength + 1), ServiceKey: "telegram", ManagementServiceKey: "management", DiscoveryMode: DiscoveryModeRegistry, DiscoveryURL: "http://management:8080"}
	if err := registry.Validate(); err != nil {
		t.Fatalf("registry config: %v", err)
	}
}

func TestManagementConfig_Validate(t *testing.T) {
	if err := (&ManagementConfig{InternalAPISecret: strOfLen(minSecretLength), ServerPort: "8080", AdminServerPort: "9090", MonitoringToken: strOfLen(minSecretLength + 1), ServiceKey: "management"}).Validate(); err != nil {
		t.Errorf("expected no error for a %d-char secret, got %v", minSecretLength, err)
	}
	if err := (&ManagementConfig{InternalAPISecret: "short"}).Validate(); err == nil {
		t.Error("expected error for a short INTERNAL_API_SECRET")
	}
	validBooking := &ManagementConfig{
		InternalAPISecret: strOfLen(minSecretLength), ServerPort: "8080", AdminServerPort: "9090", MonitoringToken: strOfLen(minSecretLength + 1), ServiceKey: "management", SportsBookingServiceURL: "http://booking:8081",
		BookingServiceKey: "eversports.primary.squash", BookingFacilityID: "76443",
		BookingFacilityUUID: "facility-uuid", BookingFacilitySlug: "facility-slug",
		BookingCutoverAck: "keyed-v2-only", BookingResponseWait: 8 * time.Second, BookingStatusPoll: 200 * time.Millisecond,
		CredentialsEncryptionKey: "configured",
	}
	if err := validBooking.Validate(); err != nil {
		t.Fatalf("expected reliable management booking config to pass: %v", err)
	}
	invalidDiscovery := *validBooking
	invalidDiscovery.DiscoveryURL = "https://user:secret@example.test/path"
	if err := invalidDiscovery.Validate(); err == nil {
		t.Fatal("expected invalid discovery bootstrap URL to fail")
	}
	registryBooking := *validBooking
	registryBooking.DiscoveryMode = DiscoveryModeRegistry
	registryBooking.DiscoveryURL = "http://management:8080"
	registryBooking.SportsBookingServiceURL = ""
	if err := registryBooking.Validate(); err != nil {
		t.Fatalf("expected registry booking configuration to pass: %v", err)
	}
	registryBooking.SportsBookingServiceURL = "http://booking:8081"
	if err := registryBooking.Validate(); err == nil {
		t.Fatal("expected registry mode with static booking URL to fail")
	}
	invalidAdmin := *validBooking
	invalidAdmin.MonitoringToken = invalidAdmin.InternalAPISecret
	if err := invalidAdmin.Validate(); err == nil {
		t.Fatal("expected management monitoring token reused as internal secret to fail")
	}
	invalidBooking := *validBooking
	invalidBooking.BookingCutoverAck = ""
	if err := invalidBooking.Validate(); err == nil {
		t.Fatal("expected booking configuration without keyed-v2 acknowledgement to fail")
	}
}

func TestBookingConfig_Validate(t *testing.T) {
	valid := func() BookingConfig {
		return BookingConfig{
			InternalAPISecret: strOfLen(minSecretLength), MonitoringSecret: strOfLen(minSecretLength + 1),
			CutoverAcknowledgement: BookingReliableCutoverAcknowledgement,
			IdentityKey:            strOfLen(64), ServerPort: "8081", AdminServerPort: "9091",
			HTTPResponseWait: 20, ExecutionTimeout: 90, ProviderCallTimeout: 20,
			FinalizationTimeout: 5, AdmissionWait: 1, ShutdownTimeout: 100,
			SingletonCheckInterval: 5, RetentionInterval: 24, RetentionAge: 90,
			SessionIdleTimeout: 10, RetentionBatchSize: 100, SessionCapacity: 32,
		}
	}
	cfg := valid()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	cfg = valid()
	cfg.CutoverAcknowledgement = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected missing reliable cutover acknowledgement to fail")
	}
	cfg = valid()
	cfg.InternalAPISecret = "short"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for a short INTERNAL_API_SECRET")
	}
	cfg = valid()
	cfg.MonitoringSecret = cfg.InternalAPISecret
	if err := cfg.Validate(); err == nil {
		t.Error("expected separate monitoring and business secrets")
	}
	cfg = valid()
	cfg.IdentityKey = "not-hex"
	if err := cfg.Validate(); err == nil {
		t.Error("expected invalid identity key error")
	}
	cfg = valid()
	cfg.ProviderCallTimeout = 21 * time.Second
	cfg.ExecutionTimeout = 90 * time.Second
	cfg.FinalizationTimeout = 5 * time.Second
	cfg.ShutdownTimeout = 100 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Error("expected provider timeout above hard maximum to fail")
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
			cfg:     WebConfig{InternalAPISecret: longSecret, JWTSecret: longSecret, ManagementServiceURL: "http://management:8080", ServerPort: "8082", AdminServerPort: "9090", MonitoringToken: strOfLen(minSecretLength + 1), ServiceKey: "web"},
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

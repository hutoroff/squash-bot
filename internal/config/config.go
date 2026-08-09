package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// minSecretLength is the minimum acceptable length for shared bearer/signing
// secrets (INTERNAL_API_SECRET, JWT_SECRET). Short secrets are practical to
// brute-force and undermine the constant-time comparisons that rely on them.
const minSecretLength = 32

// minWebhookSecretLength is the minimum acceptable length for
// TELEGRAM_WEBHOOK_SECRET. Lower than minSecretLength because it only guards
// a single header check, not a full bearer/signing secret.
const minWebhookSecretLength = 16

// TelegramConfig holds configuration for the telegram service.
type TelegramConfig struct {
	TelegramBotToken     string `env:"TELEGRAM_BOT_TOKEN,required"`
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL,required"`
	// InternalAPISecret is the shared secret used to authenticate requests to management.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	LogLevel          string `env:"LOG_LEVEL"  envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"   envDefault:"UTC"`
	// WebhookURL is the full public HTTPS URL Telegram should POST updates to.
	// When empty, the bot uses long-polling instead.
	WebhookURL string `env:"TELEGRAM_WEBHOOK_URL"`
	// WebhookSecret is sent as the X-Telegram-Bot-Api-Secret-Token header value.
	// Validated on every incoming webhook request.
	WebhookSecret string `env:"TELEGRAM_WEBHOOK_SECRET"`
	// ServerPort is the local plain-HTTP port the webhook listener binds to.
	// A TLS-terminating reverse proxy should forward to this port.
	ServerPort string `env:"SERVER_PORT" envDefault:"8083"`
}

// Validate checks invariants that the env parser cannot express: secret
// strength, and that webhook mode is never enabled without a secret token
// (an empty secret would let anyone with the webhook URL forge Updates,
// including impersonating group admins).
func (c *TelegramConfig) Validate() error {
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	if c.WebhookURL != "" && len(c.WebhookSecret) < minWebhookSecretLength {
		return fmt.Errorf("TELEGRAM_WEBHOOK_SECRET must be set (at least %d characters) when TELEGRAM_WEBHOOK_URL is set", minWebhookSecretLength)
	}
	return nil
}

// ManagementConfig holds configuration for the management service.
type ManagementConfig struct {
	DatabaseURL      string `env:"DATABASE_URL,required"`
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// InternalAPISecret is the shared secret that callers must present in the Authorization header.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	ServerPort        string `env:"SERVER_PORT"           envDefault:"8080"`
	CronPoll          string `env:"CRON_POLL"             envDefault:"*/5 * * * *"`
	LogLevel          string `env:"LOG_LEVEL"             envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"              envDefault:"UTC"`
	// SportsBookingServiceURL is the base URL of the booking service. Optional.
	// When set, the cancellation reminder will attempt to cancel unused courts automatically,
	// and the auto-booking scheduler will book courts when booking opens at midnight.
	SportsBookingServiceURL string `env:"SPORTS_BOOKING_SERVICE_URL"`
	// CredentialsEncryptionKey is a 32-byte (64 hex chars) AES-256 key used to
	// encrypt venue booking credentials at rest. Optional at startup — credential
	// operations will fail gracefully if this is not set.
	CredentialsEncryptionKey string `env:"CREDENTIALS_ENCRYPTION_KEY"`
	// CredentialErrorCooldown is how long a credential must sit out after a booking
	// error before the auto-booking job will try it again. Defaults to 24 hours.
	CredentialErrorCooldown time.Duration `env:"CREDENTIAL_ERROR_COOLDOWN" envDefault:"24h"`
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs granted the
	// is_server_owner role at startup (idempotent, grant-only bootstrap seed —
	// the DB is the source of truth once seeded; removing an ID here does not
	// revoke an existing owner).
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
	// AuditRetentionDays controls how long audit events are kept. Defaults to 365 days (1 year).
	AuditRetentionDays int `env:"AUDIT_RETENTION_DAYS" envDefault:"365"`
	// ResultWindowDays bounds how far back a past game may be for results to be
	// submitted against it: eligible when the game's local day (group timezone)
	// is today or up to this many days ago. Defaults to 14 (two weeks).
	ResultWindowDays int `env:"RESULT_WINDOW_DAYS" envDefault:"14"`
}

// Validate checks that the shared secret is strong enough to resist brute-forcing.
func (c *ManagementConfig) Validate() error {
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	return nil
}

func LoadTelegram() (*TelegramConfig, error) {
	cfg := &TelegramConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func LoadManagement() (*ManagementConfig, error) {
	cfg := &ManagementConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// BookingConfig holds configuration for the booking service.
type BookingConfig struct {
	// EversportsFacilityID is the numeric Eversports facility ID (visible in the
	// venue page URL, e.g. eversports.de/s/venue-name-76443). Required for
	// GET /api/v1/eversports/games and GET /api/v1/eversports/courts.
	EversportsFacilityID string `env:"EVERSPORTS_FACILITY_ID"`
	// EversportsFacilityUUID is the UUID of the facility (venue) used when creating
	// bookings via POST /api/v1/eversports/matches. Find it in DevTools under
	// the /checkout/api/payableitem/courtbooking request body (facilityUuid field).
	EversportsFacilityUUID string `env:"EVERSPORTS_FACILITY_UUID" envDefault:"6266968c-b0fd-4115-ad3b-ae225cc880f1"`
	// EversportsFacilitySlug is the facility slug visible in the venue page URL
	// (e.g. "squash-house-berlin-03"). Required for GET /api/v1/eversports/matches
	// and GET /api/v1/eversports/courts.
	EversportsFacilitySlug string `env:"EVERSPORTS_FACILITY_SLUG"`
	// InternalAPISecret is the shared secret that callers must present in the Authorization header.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	ServerPort        string `env:"SERVER_PORT"           envDefault:"8081"`
	LogLevel          string `env:"LOG_LEVEL"             envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"              envDefault:"UTC"`
}

// Validate checks that the shared secret is strong enough to resist brute-forcing.
func (c *BookingConfig) Validate() error {
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	return nil
}

func LoadBooking() (*BookingConfig, error) {
	cfg := &BookingConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WebConfig holds configuration for the web service.
type WebConfig struct {
	ServerPort string `env:"SERVER_PORT" envDefault:"8082"`
	LogLevel   string `env:"LOG_LEVEL"   envDefault:"INFO"`
	Timezone   string `env:"TIMEZONE"    envDefault:"UTC"`
	// TelegramBotToken is used to verify Telegram Login Widget callbacks.
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// TelegramBotName is the bot's username (without @), shown in the Login Widget.
	TelegramBotName string `env:"TELEGRAM_BOT_NAME,required"`
	// ManagementServiceURL is the base URL of the management service.
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL,required"`
	// InternalAPISecret is the shared bearer token for calling the management service.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	// JWTSecret is used to sign and verify session JWT tokens (≥32 random bytes recommended).
	JWTSecret string `env:"JWT_SECRET,required"`
}

// Validate checks that both shared secrets are strong enough to resist brute-forcing.
// A weak JWT_SECRET would let an attacker forge session tokens and take over accounts.
func (c *WebConfig) Validate() error {
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	if len(c.JWTSecret) < minSecretLength {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", minSecretLength)
	}
	return nil
}

func LoadWeb() (*WebConfig, error) {
	cfg := &WebConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadDotenv() {
	if err := godotenv.Load(); err != nil {
		slog.Debug("Error loading .env file")
	}
}

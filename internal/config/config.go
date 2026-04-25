package config

import (
	"log/slog"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// TelegramConfig holds configuration for the telegram service.
type TelegramConfig struct {
	TelegramBotToken     string `env:"TELEGRAM_BOT_TOKEN,required"`
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL,required"`
	// InternalAPISecret is the shared secret used to authenticate requests to management.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	LogLevel          string `env:"LOG_LEVEL"  envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"   envDefault:"UTC"`
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs allowed to
	// manually trigger scheduled events via /trigger.
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
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
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs recognized as
	// server owners. Used to enforce audit event visibility in GET /api/v1/audit.
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
	// AuditRetentionDays controls how long audit events are kept. Defaults to 365 days (1 year).
	AuditRetentionDays int `env:"AUDIT_RETENTION_DAYS" envDefault:"365"`
}

func LoadTelegram() (*TelegramConfig, error) {
	cfg := &TelegramConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
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

func LoadBooking() (*BookingConfig, error) {
	cfg := &BookingConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
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
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs treated as server owners.
	// Used to set the is_server_owner flag in JWT claims (UI hint only; management enforces
	// authority independently).
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
}

func LoadWeb() (*WebConfig, error) {
	cfg := &WebConfig{}
	loadDotenv()
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadDotenv() {
	if err := godotenv.Load(); err != nil {
		slog.Debug("Error loading .env file")
	}
}

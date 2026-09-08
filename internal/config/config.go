package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// minSecretLength is the minimum acceptable length for shared bearer/signing
// secrets (INTERNAL_API_SECRET, JWT_SECRET). Short secrets are practical to
// brute-force and undermine the constant-time comparisons that rely on them.
const minSecretLength = 32

// BookingReliableCutoverAcknowledgement is deliberately explicit: setting it
// confirms that operators understand reliable booking rejects keyless v1
// mutations. It is not evidence that management integration tests passed.
const BookingReliableCutoverAcknowledgement = "keyed-v2-only"
const (
	DiscoveryModeStatic   = "static"
	DiscoveryModeRegistry = "registry"
)

// minWebhookSecretLength is the minimum acceptable length for
// TELEGRAM_WEBHOOK_SECRET. Lower than minSecretLength because it only guards
// a single header check, not a full bearer/signing secret.
const minWebhookSecretLength = 16

// TelegramConfig holds configuration for the telegram service.
type TelegramConfig struct {
	TelegramBotToken     string `env:"TELEGRAM_BOT_TOKEN,required"`
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL"`
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
	ServerPort                string `env:"SERVER_PORT" envDefault:"8083"`
	AdminServerPort           string `env:"ADMIN_PORT" envDefault:"9090"`
	MonitoringToken           string `env:"MONITORING_TOKEN,required"`
	ServiceKey                string `env:"SERVICE_KEY" envDefault:"telegram"`
	ManagementServiceKey      string `env:"MANAGEMENT_SERVICE_KEY" envDefault:"management"`
	DiscoveryMode             string `env:"DISCOVERY_MODE" envDefault:"static"`
	DiscoveryURL              string `env:"DISCOVERY_URL"`
	ServiceAdvertiseHost      string `env:"SERVICE_ADVERTISE_HOST"`
	DiscoveryNetworkInterface string `env:"DISCOVERY_NETWORK_INTERFACE"`
}

// Validate checks invariants that the env parser cannot express: secret
// strength, and that webhook mode is never enabled without a secret token
// (an empty secret would let anyone with the webhook URL forge Updates,
// including impersonating group admins).
func (c *TelegramConfig) Validate() error {
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	if err := validateAdminConfig(c.ServerPort, c.AdminServerPort, c.MonitoringToken, c.InternalAPISecret, c.ServiceKey); err != nil {
		return err
	}
	if err := validateDiscoveryTarget(c.DiscoveryMode, c.DiscoveryURL, c.ManagementServiceURL, c.ManagementServiceKey, "MANAGEMENT_SERVICE_URL"); err != nil {
		return err
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
	InternalAPISecret         string `env:"INTERNAL_API_SECRET,required"`
	ServerPort                string `env:"SERVER_PORT"           envDefault:"8080"`
	AdminServerPort           string `env:"ADMIN_PORT"            envDefault:"9090"`
	MonitoringToken           string `env:"MONITORING_TOKEN,required"`
	ServiceKey                string `env:"SERVICE_KEY" envDefault:"management"`
	DiscoveryMode             string `env:"DISCOVERY_MODE" envDefault:"static"`
	DiscoveryURL              string `env:"DISCOVERY_URL"`
	ServiceAdvertiseHost      string `env:"SERVICE_ADVERTISE_HOST"`
	DiscoveryNetworkInterface string `env:"DISCOVERY_NETWORK_INTERFACE"`
	DiscoveryAllowedCIDRs     string `env:"DISCOVERY_ALLOWED_CIDRS" envDefault:"10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"`
	CronPoll                  string `env:"CRON_POLL"             envDefault:"*/5 * * * *"`
	LogLevel                  string `env:"LOG_LEVEL"             envDefault:"INFO"`
	Timezone                  string `env:"TIMEZONE"              envDefault:"UTC"`
	// SportsBookingServiceURL is the base URL of the booking service. Optional.
	// When set, the cancellation reminder will attempt to cancel unused courts automatically,
	// and the auto-booking scheduler will book courts when booking opens at midnight.
	SportsBookingServiceURL string        `env:"SPORTS_BOOKING_SERVICE_URL"`
	BookingServiceKey       string        `env:"BOOKING_SERVICE_KEY"`
	BookingFacilityID       string        `env:"EVERSPORTS_FACILITY_ID"`
	BookingFacilityUUID     string        `env:"EVERSPORTS_FACILITY_UUID"`
	BookingFacilitySlug     string        `env:"EVERSPORTS_FACILITY_SLUG"`
	BookingCutoverAck       string        `env:"BOOKING_RELIABLE_CUTOVER_ACK"`
	BookingResponseWait     time.Duration `env:"MANAGEMENT_BOOKING_RESPONSE_WAIT" envDefault:"8s"`
	BookingStatusPoll       time.Duration `env:"MANAGEMENT_BOOKING_STATUS_POLL" envDefault:"200ms"`
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
	if err := validateAdminConfig(c.ServerPort, c.AdminServerPort, c.MonitoringToken, c.InternalAPISecret, c.ServiceKey); err != nil {
		return err
	}
	if err := validateDiscoveryConfig(c.DiscoveryMode, c.DiscoveryURL); err != nil {
		return err
	}
	bookingEnabled := c.SportsBookingServiceURL != ""
	if c.DiscoveryMode == DiscoveryModeRegistry {
		if c.SportsBookingServiceURL != "" {
			return fmt.Errorf("SPORTS_BOOKING_SERVICE_URL must be empty in registry discovery mode")
		}
		bookingEnabled = c.BookingServiceKey != ""
	}
	if bookingEnabled {
		if c.BookingCutoverAck != "keyed-v2-only" {
			return fmt.Errorf("BOOKING_RELIABLE_CUTOVER_ACK must be keyed-v2-only when booking is enabled")
		}
		if c.BookingServiceKey == "" || c.BookingFacilityID == "" || c.BookingFacilityUUID == "" || c.BookingFacilitySlug == "" {
			return fmt.Errorf("booking service key and facility scope are required when booking is enabled")
		}
		if c.CredentialsEncryptionKey == "" {
			return fmt.Errorf("CREDENTIALS_ENCRYPTION_KEY is required when booking is enabled")
		}
		if c.BookingResponseWait <= 0 || c.BookingStatusPoll <= 0 || c.BookingStatusPoll > c.BookingResponseWait {
			return fmt.Errorf("management booking response wait and status poll are invalid")
		}
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

// BookingConfig holds configuration for the reliable booking service.
type BookingConfig struct {
	EversportsFacilityID   string `env:"EVERSPORTS_FACILITY_ID,required"`
	EversportsFacilityUUID string `env:"EVERSPORTS_FACILITY_UUID,required"`
	EversportsFacilitySlug string `env:"EVERSPORTS_FACILITY_SLUG,required"`
	EversportsSquashUUID   string `env:"EVERSPORTS_SQUASH_SPORT_UUID,required"`

	DatabaseURL            string `env:"BOOKING_DATABASE_URL,required"`
	IdentityKey            string `env:"BOOKING_IDENTITY_KEY,required"`
	ServiceKey             string `env:"BOOKING_SERVICE_KEY,required"`
	MonitoringSecret       string `env:"BOOKING_MONITORING_SECRET,required"`
	CutoverAcknowledgement string `env:"BOOKING_RELIABLE_CUTOVER_ACK,required"`
	InternalAPISecret      string `env:"INTERNAL_API_SECRET,required"`

	ServerPort      string `env:"SERVER_PORT" envDefault:"8081"`
	AdminServerPort string `env:"BOOKING_ADMIN_PORT" envDefault:"9091"`
	LogLevel        string `env:"LOG_LEVEL" envDefault:"INFO"`
	Timezone        string `env:"TIMEZONE" envDefault:"UTC"`

	HTTPResponseWait          time.Duration `env:"BOOKING_HTTP_RESPONSE_WAIT" envDefault:"20s"`
	ExecutionTimeout          time.Duration `env:"BOOKING_EXECUTION_TIMEOUT" envDefault:"90s"`
	ProviderCallTimeout       time.Duration `env:"BOOKING_PROVIDER_CALL_TIMEOUT" envDefault:"20s"`
	FinalizationTimeout       time.Duration `env:"BOOKING_FINALIZATION_TIMEOUT" envDefault:"5s"`
	AdmissionWait             time.Duration `env:"BOOKING_ADMISSION_WAIT" envDefault:"1s"`
	ShutdownTimeout           time.Duration `env:"BOOKING_SHUTDOWN_TIMEOUT" envDefault:"100s"`
	SingletonCheckInterval    time.Duration `env:"BOOKING_SINGLETON_CHECK_INTERVAL" envDefault:"5s"`
	RetentionInterval         time.Duration `env:"BOOKING_RETENTION_INTERVAL" envDefault:"24h"`
	RetentionAge              time.Duration `env:"BOOKING_RETENTION_AGE" envDefault:"2160h"`
	RetentionBatchSize        int           `env:"BOOKING_RETENTION_BATCH_SIZE" envDefault:"100"`
	SessionCapacity           int           `env:"BOOKING_SESSION_CAPACITY" envDefault:"32"`
	SessionIdleTimeout        time.Duration `env:"BOOKING_SESSION_IDLE_TIMEOUT" envDefault:"10m"`
	DiscoveryURL              string        `env:"DISCOVERY_URL"`
	ServiceAdvertiseHost      string        `env:"SERVICE_ADVERTISE_HOST"`
	DiscoveryNetworkInterface string        `env:"DISCOVERY_NETWORK_INTERFACE"`
}

func (c *BookingConfig) Validate() error {
	if c.CutoverAcknowledgement != BookingReliableCutoverAcknowledgement {
		return fmt.Errorf("BOOKING_RELIABLE_CUTOVER_ACK must explicitly acknowledge %q semantics", BookingReliableCutoverAcknowledgement)
	}
	if len(c.InternalAPISecret) < minSecretLength {
		return fmt.Errorf("INTERNAL_API_SECRET must be at least %d characters", minSecretLength)
	}
	if len(c.MonitoringSecret) < minSecretLength {
		return fmt.Errorf("BOOKING_MONITORING_SECRET must be at least %d characters", minSecretLength)
	}
	if c.MonitoringSecret == c.InternalAPISecret {
		return fmt.Errorf("BOOKING_MONITORING_SECRET must differ from INTERNAL_API_SECRET")
	}
	identityKey, err := hex.DecodeString(c.IdentityKey)
	if err != nil || len(identityKey) != 32 {
		return fmt.Errorf("BOOKING_IDENTITY_KEY must encode exactly 32 bytes")
	}
	serverPort, serverErr := strconv.Atoi(c.ServerPort)
	adminPort, adminErr := strconv.Atoi(c.AdminServerPort)
	if serverErr != nil || adminErr != nil || serverPort < 1 || serverPort > 65535 || adminPort < 1 || adminPort > 65535 {
		return fmt.Errorf("SERVER_PORT and BOOKING_ADMIN_PORT must be valid TCP ports")
	}
	if serverPort == adminPort {
		return fmt.Errorf("SERVER_PORT and BOOKING_ADMIN_PORT must differ")
	}
	positiveDurations := map[string]time.Duration{
		"BOOKING_HTTP_RESPONSE_WAIT": c.HTTPResponseWait, "BOOKING_EXECUTION_TIMEOUT": c.ExecutionTimeout,
		"BOOKING_PROVIDER_CALL_TIMEOUT": c.ProviderCallTimeout, "BOOKING_FINALIZATION_TIMEOUT": c.FinalizationTimeout,
		"BOOKING_ADMISSION_WAIT": c.AdmissionWait, "BOOKING_SHUTDOWN_TIMEOUT": c.ShutdownTimeout,
		"BOOKING_SINGLETON_CHECK_INTERVAL": c.SingletonCheckInterval, "BOOKING_RETENTION_INTERVAL": c.RetentionInterval,
		"BOOKING_RETENTION_AGE": c.RetentionAge, "BOOKING_SESSION_IDLE_TIMEOUT": c.SessionIdleTimeout,
	}
	for name, value := range positiveDurations {
		if value <= 0 {
			return fmt.Errorf("%s must be a positive duration", name)
		}
	}
	if c.ProviderCallTimeout > 20*time.Second || c.ProviderCallTimeout > c.ExecutionTimeout {
		return fmt.Errorf("BOOKING_PROVIDER_CALL_TIMEOUT must not exceed 20s or BOOKING_EXECUTION_TIMEOUT")
	}
	if c.ShutdownTimeout < c.ExecutionTimeout+c.FinalizationTimeout {
		return fmt.Errorf("BOOKING_SHUTDOWN_TIMEOUT must cover execution and finalization timeouts")
	}
	if c.RetentionBatchSize < 1 || c.RetentionBatchSize > 1000 {
		return fmt.Errorf("BOOKING_RETENTION_BATCH_SIZE must be between 1 and 1000")
	}
	if c.SessionCapacity < 1 {
		return fmt.Errorf("BOOKING_SESSION_CAPACITY must be positive")
	}
	return validateDiscoveryURL(c.DiscoveryURL)
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
	ServerPort                string `env:"SERVER_PORT" envDefault:"8082"`
	AdminServerPort           string `env:"ADMIN_PORT" envDefault:"9090"`
	MonitoringToken           string `env:"MONITORING_TOKEN,required"`
	ServiceKey                string `env:"SERVICE_KEY" envDefault:"web"`
	ManagementServiceKey      string `env:"MANAGEMENT_SERVICE_KEY" envDefault:"management"`
	DiscoveryMode             string `env:"DISCOVERY_MODE" envDefault:"static"`
	DiscoveryURL              string `env:"DISCOVERY_URL"`
	ServiceAdvertiseHost      string `env:"SERVICE_ADVERTISE_HOST"`
	DiscoveryNetworkInterface string `env:"DISCOVERY_NETWORK_INTERFACE"`
	LogLevel                  string `env:"LOG_LEVEL"   envDefault:"INFO"`
	Timezone                  string `env:"TIMEZONE"    envDefault:"UTC"`
	// TelegramBotToken is used to verify Telegram Login Widget callbacks.
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// TelegramBotName is the bot's username (without @), shown in the Login Widget.
	TelegramBotName string `env:"TELEGRAM_BOT_NAME,required"`
	// ManagementServiceURL is the base URL of the management service in static mode.
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL"`
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
	if err := validateAdminConfig(c.ServerPort, c.AdminServerPort, c.MonitoringToken, c.InternalAPISecret, c.ServiceKey); err != nil {
		return err
	}
	return validateDiscoveryTarget(c.DiscoveryMode, c.DiscoveryURL, c.ManagementServiceURL, c.ManagementServiceKey, "MANAGEMENT_SERVICE_URL")
}

func validateDiscoveryTarget(mode, discoveryURL, staticURL, serviceKey, staticName string) error {
	if mode == "" {
		mode = DiscoveryModeStatic
	}
	if err := validateDiscoveryConfig(mode, discoveryURL); err != nil {
		return err
	}
	if mode == DiscoveryModeRegistry {
		if staticURL != "" {
			return fmt.Errorf("%s must be empty in registry discovery mode", staticName)
		}
		if serviceKey == "" {
			return fmt.Errorf("logical service key is required in registry discovery mode")
		}
		return nil
	}
	if staticURL == "" {
		return fmt.Errorf("%s is required in static discovery mode", staticName)
	}
	return nil
}

func validateDiscoveryConfig(mode, raw string) error {
	if mode == "" {
		mode = DiscoveryModeStatic
	}
	if mode != DiscoveryModeStatic && mode != DiscoveryModeRegistry {
		return fmt.Errorf("DISCOVERY_MODE must be static or registry")
	}
	if mode == DiscoveryModeRegistry && raw == "" {
		return fmt.Errorf("DISCOVERY_URL is required in registry discovery mode")
	}
	return validateDiscoveryURL(raw)
}

func validateDiscoveryURL(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("DISCOVERY_URL must be an HTTP origin")
	}
	return nil
}

func validateAdminConfig(serverPort, adminPort, monitoringToken, internalSecret, serviceKey string) error {
	server, serverErr := strconv.Atoi(serverPort)
	admin, adminErr := strconv.Atoi(adminPort)
	if serverErr != nil || adminErr != nil || server < 1 || server > 65535 || admin < 1 || admin > 65535 || server == admin {
		return fmt.Errorf("SERVER_PORT and ADMIN_PORT must be distinct valid TCP ports")
	}
	if len(monitoringToken) < minSecretLength {
		return fmt.Errorf("MONITORING_TOKEN must be at least %d characters", minSecretLength)
	}
	if monitoringToken == internalSecret {
		return fmt.Errorf("MONITORING_TOKEN must differ from INTERNAL_API_SECRET")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`).MatchString(serviceKey) {
		return fmt.Errorf("SERVICE_KEY is invalid")
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

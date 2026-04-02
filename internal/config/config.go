package config

import (
	"log/slog"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// TelegramConfig holds configuration for the telegram-squash-bot service.
type TelegramConfig struct {
	TelegramBotToken     string `env:"TELEGRAM_BOT_TOKEN,required"`
	ManagementServiceURL string `env:"MANAGEMENT_SERVICE_URL,required"`
	// InternalAPISecret is the shared secret used to authenticate requests to squash-games-management.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	LogLevel          string `env:"LOG_LEVEL"  envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"   envDefault:"UTC"`
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs allowed to
	// manually trigger scheduled events via /trigger.
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
}

// ManagementConfig holds configuration for the squash-games-management service.
type ManagementConfig struct {
	DatabaseURL      string `env:"DATABASE_URL,required"`
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// InternalAPISecret is the shared secret that callers must present in the Authorization header.
	InternalAPISecret string `env:"INTERNAL_API_SECRET,required"`
	ServerPort        string `env:"SERVER_PORT"           envDefault:"8080"`
	CronPoll          string `env:"CRON_POLL"             envDefault:"*/5 * * * *"`
	LogLevel          string `env:"LOG_LEVEL"             envDefault:"INFO"`
	Timezone          string `env:"TIMEZONE"              envDefault:"UTC"`
	// SportsBookingServiceURL is the base URL of the sports-booking-service. Optional.
	// When set, the cancellation reminder will attempt to cancel unused courts automatically,
	// and the auto-booking scheduler will book courts when booking opens at midnight.
	SportsBookingServiceURL string `env:"SPORTS_BOOKING_SERVICE_URL"`
	// AutoBookingCourtsCount is the number of courts to book automatically at midnight
	// when booking opens. Requires SPORTS_BOOKING_SERVICE_URL to be set.
	AutoBookingCourtsCount int `env:"AUTO_BOOKING_COURTS_COUNT" envDefault:"3"`
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

// BookingConfig holds configuration for the sports-booking-service.
type BookingConfig struct {
	EversportsEmail    string `env:"EVERSPORTS_EMAIL,required"`
	EversportsPassword string `env:"EVERSPORTS_PASSWORD,required"`
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

// WebConfig holds configuration for the squash-web service.
type WebConfig struct {
	ServerPort string `env:"SERVER_PORT" envDefault:"8082"`
	LogLevel   string `env:"LOG_LEVEL"   envDefault:"INFO"`
	Timezone   string `env:"TIMEZONE"    envDefault:"UTC"`
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

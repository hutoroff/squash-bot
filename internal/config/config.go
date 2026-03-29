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

func loadDotenv() {
	if err := godotenv.Load(); err != nil {
		slog.Debug("Error loading .env file")
	}
}

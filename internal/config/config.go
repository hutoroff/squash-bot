package config

import (
	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
	"log/slog"
)

type Config struct {
	TelegramBotToken   string `env:"TELEGRAM_BOT_TOKEN,required"`
	DatabaseURL        string `env:"DATABASE_URL,required"`
	CronDayBefore      string `env:"CRON_DAY_BEFORE"       envDefault:"0 20 * * *"`
	CronDayAfter       string `env:"CRON_DAY_AFTER"        envDefault:"0 8 * * *"`
	CronWeeklyReminder string `env:"CRON_WEEKLY_REMINDER"  envDefault:"0 10 * * 1"`
	LogLevel           string `env:"LOG_LEVEL"        envDefault:"INFO"`
	Timezone           string `env:"TIMEZONE"         envDefault:"UTC"`
	// ServiceAdminIDs is a comma-separated list of Telegram user IDs allowed to
	// manually trigger scheduled events via /trigger.
	ServiceAdminIDs string `env:"SERVICE_ADMIN_IDS"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	err := godotenv.Load()
	if err != nil {
		slog.Debug("Error loading .env file")
	}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

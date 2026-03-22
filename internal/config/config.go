package config

import "github.com/caarlos0/env/v10"

type Config struct {
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	DatabaseURL      string `env:"DATABASE_URL,required"`
	GroupChatID      int64  `env:"GROUP_CHAT_ID,required"`
	AdminUserID      int64  `env:"ADMIN_USER_ID,required"`
	CronDayBefore    string `env:"CRON_DAY_BEFORE" envDefault:"0 20 * * *"`
	CronDayAfter     string `env:"CRON_DAY_AFTER"  envDefault:"0 8 * * *"`
	LogLevel         string `env:"LOG_LEVEL"        envDefault:"INFO"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

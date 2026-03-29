package models

// Group represents a Telegram group the bot is a member of.
type Group struct {
	ChatID     int64  `json:"chat_id"`
	Title      string `json:"title"`
	BotIsAdmin bool   `json:"bot_is_admin"`
	Language   string `json:"language"` // BCP-47 language code: "en", "de", "ru"
	Timezone   string `json:"timezone"` // IANA timezone name, e.g. "Europe/Berlin" (default "UTC")
}

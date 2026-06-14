package models

import "time"

// Group represents a Telegram group the bot is a member of.
type Group struct {
	ChatID                          int64      `json:"chat_id"`
	Title                           string     `json:"title"`
	BotIsAdmin                      bool       `json:"bot_is_admin"`
	Language                        string     `json:"language"`                              // BCP-47 language code: "en", "de", "ru"
	Timezone                        string     `json:"timezone"`                              // IANA timezone name, e.g. "Europe/Berlin" (default "UTC")
	ChangelogEnabled                bool       `json:"changelog_enabled"`                     // whether to send changelog announcements to this group
	LeaderboardNotificationsEnabled bool       `json:"leaderboard_notifications_enabled"`     // whether to post the leaderboard to this group after a game
	AutoBookingAllowed              bool       `json:"auto_booking_allowed"`                  // server-owner toggle: when false, auto-booking is fully disabled for this group
	LastLeaderboardPostedFor        *time.Time `json:"last_leaderboard_posted_for,omitempty"` // date of last daily leaderboard post
	AddedAt                         time.Time  `json:"added_at"`
}

package outbound

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// TelegramAPI is the subset of the Telegram Bot API used by service-layer types.
// *tgbotapi.BotAPI satisfies this interface.
type TelegramAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetChatAdministrators(config tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error)
}

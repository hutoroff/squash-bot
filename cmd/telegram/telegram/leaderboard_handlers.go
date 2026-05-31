package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

// handleCommandLeaderboard handles the /leaderboard private command.
func (b *Bot) handleCommandLeaderboard(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	groups, err := b.client.GetPlayerGroupsWithResults(ctx, msg.From.ID)
	if err != nil {
		slog.Error("handleCommandLeaderboard: get groups", "err", err)
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}
	if len(groups) == 0 {
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgLeaderboardEmpty), nil)
		return
	}
	if len(groups) == 1 {
		b.sendLeaderboard(ctx, msg.Chat.ID, groups[0].ChatID, groups[0].Title, lz, 0)
		return
	}
	// Multiple groups — show picker.
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, g := range groups {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(g.Title, fmt.Sprintf("lb_group:%d", g.ChatID)),
		))
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.sendText(msg.Chat.ID, lz.T(i18n.MsgLeaderboardPickGroup), &kb)
}

func (b *Bot) handleLbGroup(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	groupID := int64(0)
	if _, err := fmt.Sscanf(rawID, "%d", &groupID); err != nil || groupID == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	b.answerCallback(cb.ID, "")

	// Get group title.
	group, err := b.client.GetGroupByID(ctx, groupID)
	title := fmt.Sprintf("Group %d", groupID)
	if err == nil && group != nil {
		title = group.Title
	}

	b.sendLeaderboard(ctx, cb.Message.Chat.ID, groupID, title, lz, cb.Message.MessageID)
}

func (b *Bot) sendLeaderboard(ctx context.Context, chatID, groupID int64, groupTitle string, lz *i18n.Localizer, editMsgID int) {
	entries, err := b.client.GetLeaderboard(ctx, groupID)
	if err != nil {
		slog.Error("sendLeaderboard: get leaderboard", "err", err)
		if editMsgID != 0 {
			b.editText(chatID, editMsgID, lz.T(i18n.MsgSomethingWentWrong), nil)
		} else {
			b.sendText(chatID, lz.T(i18n.MsgSomethingWentWrong), nil)
		}
		return
	}
	text := renderLeaderboard(entries, groupTitle, lz)
	if editMsgID != 0 {
		b.editText(chatID, editMsgID, text, nil)
	} else {
		b.sendText(chatID, text, nil)
	}
}

func renderLeaderboard(entries []client.LeaderboardEntry, groupTitle string, lz *i18n.Localizer) string {
	if len(entries) == 0 {
		return lz.T(i18n.MsgLeaderboardEmpty)
	}
	var sb strings.Builder
	sb.WriteString(lz.Tf(i18n.MsgLeaderboardTitle, groupTitle))
	sb.WriteString("\n\n")
	for _, e := range entries {
		name := ""
		if e.Player != nil {
			name = playerModelDisplayName(e.Player)
		}
		delta := ""
		if e.DeltaToday > 0.5 {
			delta = fmt.Sprintf("   ▲+%.0f", e.DeltaToday)
		} else if e.DeltaToday < -0.5 {
			delta = fmt.Sprintf("   ▼%.0f", e.DeltaToday)
		}
		sb.WriteString(fmt.Sprintf("%d.  %-16s %4.0f (%d%s)%s\n",
			e.Rank, name, e.Rating, e.GamesPlayed, lz.T(i18n.MsgLeaderboardGamesShort), delta))
	}
	return sb.String()
}

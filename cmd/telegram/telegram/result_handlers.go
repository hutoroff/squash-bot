package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── Result wizard types ───────────────────────────────────────────────────────

type resultWizardStep int

const (
	resultStepGroup    resultWizardStep = iota
	resultStepGame                      // pick a completed game
	resultStepOpponent                  // pick the opponent from that game
	resultStepWinner                    // 🏆 me / opp / draw
	resultStepScore                     // optional N:M or skip
	resultStepPreview                   // review and submit
)

type resultWizard struct {
	step        resultWizardStep
	groupID     int64
	gameID      int64
	gameLabel   string                   // cached for preview
	opponent    *models.Player           // cached for preview
	winnerID    *int64                   // nil = draw
	winnerLabel string                   // "me" | "@user" for display
	score       string                   // "" if skipped
	candGames   []models.PlayerGame      // memoized recent-games list
	playerCache map[int64]*models.Player // playerID → Player (populated at opponent-pick step)
}

var scoreRe = regexp.MustCompile(`^\d+:\d+$`)

// ── /result command ───────────────────────────────────────────────────────────

func (b *Bot) handleCommandResult(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	// Only works in private chat.
	// Look up the groups the user belongs to (has any participation).
	groups, err := b.client.GetGroups(ctx)
	if err != nil {
		slog.Error("handleCommandResult: get groups", "err", err)
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}

	// Filter to groups where the player has participated.
	var playerGroups []models.Group
	for _, g := range groups {
		games, err := b.client.GetRecentCompletedGames(ctx, msg.From.ID, g.ChatID)
		if err != nil {
			continue
		}
		if len(games) > 0 {
			playerGroups = append(playerGroups, g)
		}
	}

	wiz := &resultWizard{}
	b.pendingResultWizard.Store(msg.Chat.ID, wiz)

	if len(playerGroups) == 0 {
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgResultNotInGame), nil)
		b.pendingResultWizard.Delete(msg.Chat.ID)
		return
	}

	if len(playerGroups) == 1 {
		// Auto-pick the only group and go straight to game picker.
		wiz.groupID = playerGroups[0].ChatID
		wiz.step = resultStepGame
		b.sendResultGamePicker(ctx, msg.Chat.ID, msg.From.ID, wiz, lz, 0)
		return
	}

	// Multiple groups — show group picker.
	wiz.step = resultStepGroup
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, g := range playerGroups {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(g.Title, fmt.Sprintf("res_group:%d", g.ChatID)),
		))
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.sendText(msg.Chat.ID, lz.T(i18n.MsgResultStepPickGroup), &kb)
}

// ── Step handlers (callbacks) ─────────────────────────────────────────────────

func (b *Bot) handleResultPickGroup(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	groupID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)
	wiz.groupID = groupID
	wiz.step = resultStepGame

	b.answerCallback(cb.ID, "")
	b.sendResultGamePicker(ctx, cb.Message.Chat.ID, cb.From.ID, wiz, lz, cb.Message.MessageID)
}

func (b *Bot) sendResultGamePicker(ctx context.Context, chatID, tgID int64, wiz *resultWizard, lz *i18n.Localizer, editMsgID int) {
	games, err := b.client.GetRecentCompletedGames(ctx, tgID, wiz.groupID)
	if err != nil {
		slog.Error("sendResultGamePicker: get games", "err", err)
		b.sendText(chatID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}
	if len(games) == 0 {
		b.sendText(chatID, lz.T(i18n.MsgResultErrNoCompletedGames), nil)
		b.pendingResultWizard.Delete(chatID)
		return
	}
	wiz.candGames = games

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, g := range games {
		label := g.GameDate.Format("Mon 02 Jan")
		if g.VenueName != "" {
			label += " · " + g.VenueName
		}
		label += fmt.Sprintf(" · %d players", g.ParticipantCount)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("res_game:%d", g.ID)),
		))
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)

	if editMsgID != 0 {
		b.editText(chatID, editMsgID, lz.T(i18n.MsgResultStepPickGame), &kb)
	} else {
		b.sendText(chatID, lz.T(i18n.MsgResultStepPickGame), &kb)
	}
}

func (b *Bot) handleResultPickGame(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	gameID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)
	wiz.gameID = gameID

	// Cache the game label from candGames.
	for _, g := range wiz.candGames {
		if g.ID == gameID {
			label := g.GameDate.Format("Mon 02 Jan")
			if g.VenueName != "" {
				label += " · " + g.VenueName
			}
			wiz.gameLabel = label
			break
		}
	}
	wiz.step = resultStepOpponent

	b.answerCallback(cb.ID, "")
	b.sendResultOpponentPicker(ctx, cb.Message.Chat.ID, cb.From.ID, cb.Message.MessageID, wiz, lz)
}

func (b *Bot) sendResultOpponentPicker(ctx context.Context, chatID, tgID int64, msgID int, wiz *resultWizard, lz *i18n.Localizer) {
	parts, err := b.client.GetParticipations(ctx, wiz.gameID)
	if err != nil {
		slog.Error("sendResultOpponentPicker: get participations", "err", err)
		b.editText(chatID, msgID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}

	// Resolve self player ID.
	selfPlayer, err := b.resolvePlayer(ctx, tgID)
	if err != nil || selfPlayer == nil {
		b.editText(chatID, msgID, lz.T(i18n.MsgResultNotInGame), nil)
		b.pendingResultWizard.Delete(chatID)
		return
	}

	// Build a map playerID → player for quick opponent lookup after selection.
	playerMap := make(map[int64]*models.Player, len(parts))
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range parts {
		if p.Status != models.StatusRegistered {
			continue
		}
		if p.PlayerID == selfPlayer.ID {
			continue
		}
		if p.Player != nil {
			playerMap[p.PlayerID] = p.Player
		}
		label := participationDisplayName(p)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("res_opp:%d", p.PlayerID)),
		))
	}
	if len(rows) == 0 {
		b.editText(chatID, msgID, lz.T(i18n.MsgResultNotInGame), nil)
		b.pendingResultWizard.Delete(chatID)
		return
	}
	// Store playerMap in the wizard so handleResultPickOpponent can look up player data.
	raw2, ok2 := b.pendingResultWizard.Load(chatID)
	if ok2 {
		wiz2 := raw2.(*resultWizard)
		wiz2.playerCache = playerMap
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, msgID, lz.T(i18n.MsgResultStepPickOpponent), &kb)
}

func (b *Bot) handleResultPickOpponent(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	opponentID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)

	// Get the opponent player record from the cached player map (populated during opponent-picker rendering).
	opp, ok2 := wiz.playerCache[opponentID]
	if !ok2 {
		// Fallback: create a minimal stub so the wizard can continue.
		opp = &models.Player{ID: opponentID}
	}
	wiz.opponent = opp
	wiz.step = resultStepWinner

	b.answerCallback(cb.ID, "")
	b.renderWinnerPicker(cb.Message.Chat.ID, cb.Message.MessageID, wiz, lz)
}

func (b *Bot) renderWinnerPicker(chatID int64, msgID int, wiz *resultWizard, lz *i18n.Localizer) {
	oppDisplay := playerModelDisplayName(wiz.opponent)
	rows := [][]tgbotapi.InlineKeyboardButton{
		{
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.MsgResultWinnerMe), "res_winner:me"),
			tgbotapi.NewInlineKeyboardButtonData(lz.Tf(i18n.MsgResultWinnerOpp, oppDisplay), "res_winner:opp"),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.MsgResultWinnerDraw), "res_winner:draw"),
		},
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, msgID, lz.T(i18n.MsgResultStepPickWinner), &kb)
}

func (b *Bot) handleResultPickWinner(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)

	selfPlayer, err := b.resolvePlayer(ctx, cb.From.ID)
	if err != nil || selfPlayer == nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	switch rawID {
	case "me":
		wiz.winnerID = &selfPlayer.ID
		wiz.winnerLabel = lz.T(i18n.MsgResultWinnerMe)
	case "opp":
		oppID := wiz.opponent.ID
		wiz.winnerID = &oppID
		wiz.winnerLabel = lz.Tf(i18n.MsgResultWinnerOpp, playerModelDisplayName(wiz.opponent))
	case "draw":
		wiz.winnerID = nil
		wiz.winnerLabel = lz.T(i18n.MsgResultWinnerDraw)
	default:
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	wiz.step = resultStepScore

	b.answerCallback(cb.ID, "")
	b.renderScoreStep(cb.Message.Chat.ID, cb.Message.MessageID, wiz, lz)
}

func (b *Bot) renderScoreStep(chatID int64, msgID int, wiz *resultWizard, lz *i18n.Localizer) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultScoreSkip), "res_score_skip:_"),
		),
	)
	b.editText(chatID, msgID, lz.T(i18n.MsgResultStepEnterScore), &kb)
}

func (b *Bot) handleResultScoreSkip(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
	lz := b.userLocalizer(ctx, cb.From)

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)
	wiz.score = ""
	wiz.step = resultStepPreview

	b.answerCallback(cb.ID, "")
	b.renderResultPreview(cb.Message.Chat.ID, cb.Message.MessageID, wiz, lz)
}

// processResultWizard handles free-text input at the score step.
func (b *Bot) processResultWizard(ctx context.Context, msg *tgbotapi.Message, wiz *resultWizard) {
	lz := b.userLocalizer(ctx, msg.From)

	if wiz.step != resultStepScore {
		return
	}

	score := strings.TrimSpace(msg.Text)
	if !scoreRe.MatchString(score) {
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgResultErrBadScore), nil)
		return
	}

	// Validate winner's number ≥ loser's if a winner is set.
	if wiz.winnerID != nil {
		selfPlayer, _ := b.resolvePlayer(ctx, msg.From.ID)
		if err := validateResultScore(score, wiz, selfPlayer); err != nil {
			b.sendText(msg.Chat.ID, lz.T(i18n.MsgResultErrBadScore), nil)
			return
		}
	}

	wiz.score = score
	wiz.step = resultStepPreview

	// Send a new message for the preview (can't edit the previous inline-button message with text input).
	b.renderResultPreviewNew(msg.Chat.ID, wiz, lz)
}

func (b *Bot) renderResultPreview(chatID int64, msgID int, wiz *resultWizard, lz *i18n.Localizer) {
	oppDisplay := playerModelDisplayName(wiz.opponent)
	scoreDisplay := wiz.score
	if scoreDisplay == "" {
		scoreDisplay = "—"
	}

	text := lz.Tf(i18n.MsgResultPreview, escapeMarkdown(wiz.gameLabel), escapeMarkdown(oppDisplay), escapeMarkdown(wiz.winnerLabel), scoreDisplay)
	kb := buildResultPreviewKeyboard(lz)
	b.editText(chatID, msgID, text, kb)
}

func (b *Bot) renderResultPreviewNew(chatID int64, wiz *resultWizard, lz *i18n.Localizer) {
	oppDisplay := playerModelDisplayName(wiz.opponent)
	scoreDisplay := wiz.score
	if scoreDisplay == "" {
		scoreDisplay = "—"
	}

	text := lz.Tf(i18n.MsgResultPreview, escapeMarkdown(wiz.gameLabel), escapeMarkdown(oppDisplay), escapeMarkdown(wiz.winnerLabel), scoreDisplay)
	kb := buildResultPreviewKeyboard(lz)
	b.sendText(chatID, text, kb)
}

func buildResultPreviewKeyboard(lz *i18n.Localizer) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultEditGame), "res_edit:game"),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultEditOpponent), "res_edit:opp"),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultEditWinner), "res_edit:winner"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultEditScore), "res_edit:score"),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultCancel), "res_cancel:_"),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultSubmit), "res_submit:_"),
		),
	)
	return &kb
}

func (b *Bot) handleResultEdit(ctx context.Context, cb *tgbotapi.CallbackQuery, field string) {
	lz := b.userLocalizer(ctx, cb.From)

	raw, ok := b.pendingResultWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)

	b.answerCallback(cb.ID, "")
	switch field {
	case "game":
		wiz.step = resultStepGame
		b.sendResultGamePicker(ctx, cb.Message.Chat.ID, cb.From.ID, wiz, lz, cb.Message.MessageID)
	case "opp":
		wiz.step = resultStepOpponent
		b.sendResultOpponentPicker(ctx, cb.Message.Chat.ID, cb.From.ID, cb.Message.MessageID, wiz, lz)
	case "winner":
		wiz.step = resultStepWinner
		b.renderWinnerPicker(cb.Message.Chat.ID, cb.Message.MessageID, wiz, lz)
	case "score":
		wiz.step = resultStepScore
		b.renderScoreStep(cb.Message.Chat.ID, cb.Message.MessageID, wiz, lz)
	}
}

func (b *Bot) handleResultCancel(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
	lz := b.userLocalizer(ctx, cb.From)
	b.pendingResultWizard.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.BtnResultCancel)+" ✓", nil)
}

func (b *Bot) handleResultSubmit(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
	lz := b.userLocalizer(ctx, cb.From)

	raw, ok := b.pendingResultWizard.LoadAndDelete(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*resultWizard)

	result, err := b.client.SubmitGameResult(ctx,
		wiz.gameID, cb.From.ID, wiz.opponent.ID,
		wiz.winnerID, wiz.score, actorDisplayFrom(cb.From),
	)
	if err != nil {
		slog.Error("handleResultSubmit: submit", "err", err)
		b.answerCallback(cb.ID, "")
		b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}

	b.answerCallback(cb.ID, "")

	oppDisplay := playerModelDisplayName(wiz.opponent)

	// Try to DM the opponent.
	approvalText := b.buildApprovalCardText(ctx, result, wiz, actorDisplayFrom(cb.From), lz)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultApprove), fmt.Sprintf("res_approve:%d", result.ID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultReject), fmt.Sprintf("res_reject:%d", result.ID)),
		),
	)

	oppMsg := tgbotapi.NewMessage(wiz.opponent.TelegramID, approvalText)
	oppMsg.ParseMode = "Markdown"
	oppMsg.ReplyMarkup = kb
	sentMsg, dmErr := b.api.Send(oppMsg)

	if dmErr != nil {
		// Any failure to deliver the approval DM cancels the result so it cannot silently auto-approve.
		slog.Warn("handleResultSubmit: send approval DM", "err", dmErr)
		_, _ = b.client.CancelGameResult(ctx, result.ID, cb.From.ID, actorDisplayFrom(cb.From))
		b.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			lz.Tf(i18n.MsgResultDMUnreachable, escapeMarkdown(oppDisplay)), nil)
		return
	}

	// Store the opponent DM info so the auto-approve job can edit it.
	_ = b.client.SetGameResultApprovalMessage(ctx, result.ID, wiz.opponent.TelegramID, sentMsg.MessageID)

	// Update the wizard message to show "submitted, waiting".
	withdrawKB := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultWithdraw), fmt.Sprintf("res_withdraw:%d", result.ID)),
		),
	)
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgResultSubmitted, escapeMarkdown(oppDisplay)), &withdrawKB)
}

func (b *Bot) buildApprovalCardText(ctx context.Context, result *client.GameResultDTO, wiz *resultWizard, authorDisplay string, lz *i18n.Localizer) string {
	escapedAuthor := escapeMarkdown(authorDisplay)
	escapedGameLabel := escapeMarkdown(wiz.gameLabel)

	var outcomeLabel string
	if result.WinnerID == nil {
		outcomeLabel = lz.T(i18n.MsgResultWinnerDraw)
	} else if *result.WinnerID == wiz.opponent.ID {
		outcomeLabel = lz.T(i18n.MsgResultWinnerMe) // from opponent's perspective
	} else {
		// Author won — show the author's name from the opponent's perspective.
		outcomeLabel = lz.Tf(i18n.MsgResultWinnerOpp, escapedAuthor)
	}

	scoreDisplay := result.Score
	if scoreDisplay == "" {
		scoreDisplay = "—"
	}

	autoApproveStr := ""
	if result.AutoApproveAt != nil {
		t, err := time.Parse(time.RFC3339, *result.AutoApproveAt)
		if err == nil {
			autoApproveStr = formatDeadline(lz, t)
		}
	}

	baseText := lz.Tf(i18n.MsgResultApprovalRequest,
		escapedAuthor, escapedGameLabel, outcomeLabel, scoreDisplay)
	if autoApproveStr != "" {
		baseText += lz.Tf(i18n.MsgResultDeadlineLine, autoApproveStr)
	}
	return baseText
}

// buildApprovedCardText builds the card text shown to the opponent after they approve a result.
// Unlike buildApprovalCardText it derives all fields from the DTO (no wizard available on the approver side).
func (b *Bot) buildApprovedCardText(ctx context.Context, result *client.GameResultDTO, lz *i18n.Localizer) string {
	authorDisplay := fmt.Sprintf("player#%d", result.AuthorID)
	if result.Author != nil {
		authorDisplay = playerModelDisplayName(result.Author)
	}

	gameLabel := fmt.Sprintf("game #%d", result.GameID)
	if game, err := b.client.GetGameByID(ctx, result.GameID); err == nil {
		gameLabel = game.GameDate.Format("Mon 02 Jan")
	} else {
		slog.Warn("buildApprovedCardText: fetch game", "gameID", result.GameID, "err", err)
	}

	escapedAuthor := escapeMarkdown(authorDisplay)
	var outcomeLabel string
	if result.WinnerID == nil {
		outcomeLabel = lz.T(i18n.MsgResultWinnerDraw)
	} else if *result.WinnerID == result.OpponentID {
		outcomeLabel = lz.T(i18n.MsgResultWinnerMe)
	} else {
		outcomeLabel = lz.Tf(i18n.MsgResultWinnerOpp, escapedAuthor)
	}

	scoreDisplay := result.Score
	if scoreDisplay == "" {
		scoreDisplay = "—"
	}

	return lz.Tf(i18n.MsgResultApprovedCard, escapedAuthor, escapeMarkdown(gameLabel), outcomeLabel, scoreDisplay)
}

// handleResultApprove handles the Approve callback from the opponent DM.
func (b *Bot) handleResultApprove(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	result, err := b.client.ApproveGameResult(ctx, id, cb.From.ID, actorDisplayFrom(cb.From))
	if err != nil {
		if errors.Is(err, client.ErrGameResultNotPending) {
			b.answerCallback(cb.ID, lz.T(i18n.MsgResultAlreadyDecided))
			b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgResultAlreadyDecided)+".", nil)
			return
		}
		slog.Error("handleResultApprove", "err", err, "id", id)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	b.answerCallback(cb.ID, "")

	// Edit the opponent DM card with the match details.
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID,
		b.buildApprovedCardText(ctx, result, lz), nil)

	// DM the author.
	deciderDisplay := actorDisplayFrom(cb.From)
	if result.Author != nil && result.Author.TelegramID != 0 {
		m := tgbotapi.NewMessage(result.Author.TelegramID, lz.Tf(i18n.MsgResultApproved, deciderDisplay))
		if _, err := b.api.Send(m); err != nil {
			slog.Warn("handleResultApprove: dm author", "err", err)
		}
	}
}

// handleResultReject handles the Reject callback from the opponent DM.
func (b *Bot) handleResultReject(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	result, err := b.client.RejectGameResult(ctx, id, cb.From.ID, actorDisplayFrom(cb.From))
	if err != nil {
		if errors.Is(err, client.ErrGameResultNotPending) {
			b.answerCallback(cb.ID, lz.T(i18n.MsgResultAlreadyDecided))
			return
		}
		slog.Error("handleResultReject", "err", err, "id", id)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	b.answerCallback(cb.ID, "")
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgResultRejectedOn, lz.FormatUpdatedAt(time.Now())), nil)

	// DM the author with a resubmit button.
	deciderDisplay := actorDisplayFrom(cb.From)
	if result.Author != nil && result.Author.TelegramID != 0 {
		resubmitKB := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnResultResubmit), fmt.Sprintf("res_resubmit:%d", result.ID)),
			),
		)
		m := tgbotapi.NewMessage(result.Author.TelegramID, lz.Tf(i18n.MsgResultRejected, deciderDisplay))
		m.ReplyMarkup = resubmitKB
		if _, err := b.api.Send(m); err != nil {
			slog.Warn("handleResultReject: dm author", "err", err)
		}
	}
}

// handleResultWithdraw handles the Withdraw button the author presses to cancel a pending result.
func (b *Bot) handleResultWithdraw(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	result, err := b.client.CancelGameResult(ctx, id, cb.From.ID, actorDisplayFrom(cb.From))
	if err != nil {
		if errors.Is(err, client.ErrGameResultNotPending) {
			b.answerCallback(cb.ID, lz.T(i18n.MsgResultAlreadyDecided))
			return
		}
		slog.Error("handleResultWithdraw", "err", err, "id", id)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	b.answerCallback(cb.ID, "")
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgResultWithdrawn), nil)

	// Edit the opponent's DM card if we have the info.
	if result.ApprovalChatID != nil && result.ApprovalMessageID != nil {
		oppMsg := tgbotapi.NewEditMessageText(*result.ApprovalChatID, *result.ApprovalMessageID, lz.T(i18n.MsgResultWithdrawn))
		if _, err := b.api.Request(oppMsg); err != nil {
			slog.Warn("handleResultWithdraw: edit opponent card", "err", err)
		}
	}
}

// handleResultResubmit pre-loads the wizard from a rejected result.
func (b *Bot) handleResultResubmit(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(ctx, cb.From)
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	result, err := b.client.GetGameResult(ctx, id)
	if err != nil {
		slog.Error("handleResultResubmit: get result", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if result.Opponent == nil || result.Opponent.TelegramID == 0 {
		slog.Error("handleResultResubmit: opponent player missing in result", "id", id)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	opp := result.Opponent

	// Best-effort: fetch the game to build a human-readable label.
	gameLabel := ""
	if game, err := b.client.GetGameByID(ctx, result.GameID); err == nil {
		gameLabel = game.GameDate.Format("Mon 02 Jan")
	}

	wiz := &resultWizard{
		step:      resultStepPreview,
		groupID:   result.GroupID,
		gameID:    result.GameID,
		gameLabel: gameLabel,
		opponent:  opp,
		score:     result.Score,
		winnerID:  result.WinnerID,
	}
	if wiz.winnerID == nil {
		wiz.winnerLabel = lz.T(i18n.MsgResultWinnerDraw)
	} else if *wiz.winnerID == opp.ID {
		wiz.winnerLabel = lz.Tf(i18n.MsgResultWinnerOpp, playerModelDisplayName(opp))
	} else {
		wiz.winnerLabel = lz.T(i18n.MsgResultWinnerMe)
	}

	b.pendingResultWizard.Store(cb.Message.Chat.ID, wiz)

	b.answerCallback(cb.ID, "")
	b.renderResultPreviewNew(cb.Message.Chat.ID, wiz, lz)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// resolvePlayer fetches a Player by Telegram ID, returning nil if not found.
func (b *Bot) resolvePlayer(ctx context.Context, tgID int64) (*models.Player, error) {
	p, err := b.client.GetPlayerByTelegramID(ctx, tgID)
	if err != nil {
		// 404 means the player hasn't joined any game yet.
		var httpErr *client.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

// participationDisplayName returns a display string for a game participation.
func participationDisplayName(p *models.GameParticipation) string {
	if p.Player == nil {
		return fmt.Sprintf("player#%d", p.PlayerID)
	}
	return playerModelDisplayName(p.Player)
}

// playerModelDisplayName returns a display string for a Player model.
func playerModelDisplayName(p *models.Player) string {
	if p.Username != nil && *p.Username != "" {
		return "@" + *p.Username
	}
	name := ""
	if p.FirstName != nil {
		name = *p.FirstName
	}
	if p.LastName != nil && *p.LastName != "" {
		if name != "" {
			name += " "
		}
		name += *p.LastName
	}
	if name != "" {
		return name
	}
	return fmt.Sprintf("player#%d", p.ID)
}

// validateResultScore checks that the score is consistent with the winner.
func validateResultScore(score string, wiz *resultWizard, self *models.Player) error {
	if score == "" || wiz.winnerID == nil {
		return nil
	}
	parts := strings.SplitN(score, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("bad score format")
	}
	left, err1 := strconv.Atoi(parts[0])
	right, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("bad score numbers")
	}
	if self != nil && *wiz.winnerID == self.ID {
		if left < right {
			return fmt.Errorf("winner's score must be ≥ loser's")
		}
	} else if wiz.opponent != nil && *wiz.winnerID == wiz.opponent.ID {
		if right < left {
			return fmt.Errorf("winner's score must be ≥ loser's")
		}
	}
	return nil
}

// formatDeadline produces a locale-aware deadline string such as "Tuesday, 13 May at 11:00".
func formatDeadline(lz *i18n.Localizer, t time.Time) string {
	return lz.FormatGameDate(t) + " at " + t.Format("15:04")
}

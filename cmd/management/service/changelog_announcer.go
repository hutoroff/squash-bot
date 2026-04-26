package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/changelogs"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/jackc/pgx/v5"
)

const lastChangelogVersionKey = "last_changelog_version"

// AnnounceChangelog sends the changelog for the given version to all groups that have changelog_enabled=true.
// It is a one-shot call: safe to run on every startup; deduplication is done via service_state.
// If no changelog file exists for the version, the version is recorded and the function returns silently.
func AnnounceChangelog(
	ctx context.Context,
	api TelegramAPI,
	groupRepo GroupRepository,
	stateRepo ServiceStateRepository,
	loc *time.Location,
	logger *slog.Logger,
	version string,
) {
	announced, err := stateRepo.Get(ctx, lastChangelogVersionKey)
	if err != nil && err != pgx.ErrNoRows {
		logger.Error("changelog announcer: read state", "err", err)
		return
	}
	if announced == version {
		return
	}

	content, err := changelogs.Read(version)
	if err != nil {
		// No changelog for this version — record it so we don't check again on next restart.
		if setErr := stateRepo.Set(ctx, lastChangelogVersionKey, version); setErr != nil {
			logger.Error("changelog announcer: record version (no file)", "err", setErr)
		}
		return
	}

	groups, err := groupRepo.GetAll(ctx)
	if err != nil {
		logger.Error("changelog announcer: get groups", "err", err)
		return
	}

	for _, g := range groups {
		if !g.ChangelogEnabled {
			continue
		}
		lz := i18n.New(i18n.Normalize(g.Language))
		text := formatChangelogMessage(lz, version, content)
		msg := tgbotapi.NewMessage(g.ChatID, text)
		msg.ParseMode = tgbotapi.ModeMarkdown
		if _, err := api.Send(msg); err != nil {
			logger.Error("changelog announcer: send message", "chat_id", g.ChatID, "err", err)
		}
	}

	if err := stateRepo.Set(ctx, lastChangelogVersionKey, version); err != nil {
		logger.Error("changelog announcer: record version", "err", err)
	}
	logger.Info("changelog announced", "version", version, "groups", len(groups))
}

// formatChangelogMessage builds a localised Telegram message from raw markdown changelog content.
// The content is expected to have optional "## Features" and "## Fixes" sections with bullet items.
func formatChangelogMessage(lz *i18n.Localizer, version, content string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(lz.T(i18n.MsgChangelogHeader), version))
	sb.WriteString("\n\n")

	features := extractSection(content, "## Features")
	fixes := extractSection(content, "## Fixes")

	if len(features) > 0 {
		sb.WriteString(lz.T(i18n.MsgChangelogFeatures))
		sb.WriteString("\n")
		for _, item := range features {
			sb.WriteString("• ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(fixes) > 0 {
		if len(features) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(lz.T(i18n.MsgChangelogFixes))
		sb.WriteString("\n")
		for _, item := range fixes {
			sb.WriteString("• ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// extractSection returns the bullet items under the named section header.
func extractSection(content, header string) []string {
	idx := strings.Index(content, header)
	if idx < 0 {
		return nil
	}
	rest := content[idx+len(header):]
	// Find next section or end
	end := strings.Index(rest, "\n## ")
	if end >= 0 {
		rest = rest[:end]
	}
	var items []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			items = append(items, strings.TrimPrefix(line, "- "))
		}
	}
	return items
}

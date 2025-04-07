package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"xray-vpn-tg-bot/internal/domain"

	gobot "github.com/go-telegram/bot"
)

// Context key type
type contextKey string

const userContextKey contextKey = "user"

// --- Utility Functions --- //

// sendUserMessage sends a plain message to the user.
func (b *Bot) sendUserMessage(ctx context.Context, bot *gobot.Bot, chatID int64, message string) {
	if chatID == 0 {
		b.logger.ErrorContext(ctx, "sendUserMessage called with chatID 0")
		return
	}
	params := &gobot.SendMessageParams{
		ChatID: chatID,
		Text:   message,
	}
	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send user message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// sendUserWarning sends a warning message (prefixed with ⚠️) to the user.
func (b *Bot) sendUserWarning(ctx context.Context, bot *gobot.Bot, chatID int64, message string) {
	if chatID == 0 {
		b.logger.ErrorContext(ctx, "sendUserWarning called with chatID 0")
		return
	}
	params := &gobot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("⚠️ %s", message),
	}
	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send user warning", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// sendUserError sends an error message (prefixed with ❗️) to the user.
func (b *Bot) sendUserError(ctx context.Context, bot *gobot.Bot, chatID int64, message string) {
	if chatID == 0 {
		b.logger.ErrorContext(ctx, "sendUserError called with chatID 0")
		return
	}
	b.logger.WarnContext(ctx, "Sending user-facing error", slog.Int64("chat_id", chatID), slog.String("error_message", message))
	params := &gobot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("❗️ %s", message),
	}
	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send user error message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// sendInternalError sends a generic internal error message.
func (b *Bot) sendInternalError(ctx context.Context, bot *gobot.Bot, chatID int64) {
	if chatID == 0 {
		b.logger.ErrorContext(ctx, "sendInternalError called with chatID 0")
		return
	}
	params := &gobot.SendMessageParams{
		ChatID: chatID,
		Text:   "Произошла внутренняя ошибка сервера. Пожалуйста, попробуйте позже или обратитесь в поддержку.",
	}
	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send internal error message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// SetUserInContext stores the user in the Go context.
func SetUserInContext(ctx context.Context, user *domain.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext retrieves the user from the Go context.
// Returns nil if not found or type assertion fails.
func UserFromContext(ctx context.Context) *domain.User {
	if ctx == nil {
		return nil
	}
	if user, ok := ctx.Value(userContextKey).(*domain.User); ok {
		return user
	}
	return nil
}

// escapeMarkdownV2 escapes characters reserved by MarkdownV2.
func escapeMarkdownV2(s string) string {
	// List of characters to escape
	chars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	for _, char := range chars {
		s = strings.ReplaceAll(s, char, "\\"+char)
	}
	return s
}

// Helper to translate status (can be expanded)
func translateStatus(status domain.SubscriptionStatus) string {
	switch status {
	case domain.SubscriptionStatusActive:
		return "Активна ✅"
	case domain.SubscriptionStatusExpired:
		return "Истекла ⏳"
	case domain.SubscriptionStatusCancelled:
		return "Отменена ❌"
	case domain.SubscriptionStatusDepleted:
		return "Трафик исчерпан 💨"
	default:
		return string(status)
	}
}

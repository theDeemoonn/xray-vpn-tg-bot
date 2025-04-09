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

// escapeMarkdownV2 экранирует специальные символы Markdown V2.
func escapeMarkdownV2(text string) string {
	// MarkdownV2 требует экранирования следующих символов:
	// _, *, [, ], (, ), ~, `, >, #, +, -, =, |, {, }, ., !
	specialChars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}

	// Проверка: может быть, строка уже содержит экранированные символы?
	if strings.Contains(text, "\\") {
		// Если строка уже содержит обратные слеши, мы должны быть аккуратными
		// Сначала заменяем обратные слеши на временный маркер
		text = strings.ReplaceAll(text, "\\", "\\\\")
	}

	// Экранируем каждый специальный символ
	for _, char := range specialChars {
		text = strings.ReplaceAll(text, char, "\\"+char)
	}

	return text
}

// Helper to translate status with additional 3x-ui information
func translateStatusWithXUI(status domain.SubscriptionStatus, isEnabledInXUI bool, trafficUsed int64, trafficLimit int64) string {
	// Сначала проверяем состояние в 3x-ui
	if !isEnabledInXUI {
		return "Отключена в 3x-ui ⚠️"
	}

	// Проверяем трафик, если он ограничен
	if trafficLimit > 0 && trafficUsed >= trafficLimit {
		return "Трафик исчерпан 💨"
	}

	// Используем базовый перевод статуса
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

// --- Методы для работы с состояниями диалогов ---

// getDialogState возвращает текущее состояние диалога для пользователя
func (b *Bot) getDialogState(userID int64) map[string]interface{} {
	state, exists := b.dialogStates[userID]
	if !exists {
		state = make(map[string]interface{})
		b.dialogStates[userID] = state
	}
	return state
}

// setDialogState устанавливает состояние диалога для пользователя
func (b *Bot) setDialogState(userID int64, key string, value interface{}) {
	state := b.getDialogState(userID)
	state[key] = value
}

// getDialogStateString возвращает строковое значение из состояния диалога
func (b *Bot) getDialogStateString(userID int64, key string) string {
	state := b.getDialogState(userID)
	value, exists := state[key]
	if !exists {
		return ""
	}
	strValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strValue
}

// getDialogStateInt возвращает целочисленное значение из состояния диалога
func (b *Bot) getDialogStateInt(userID int64, key string) int {
	state := b.getDialogState(userID)
	value, exists := state[key]
	if !exists {
		return 0
	}
	intValue, ok := value.(int)
	if !ok {
		return 0
	}
	return intValue
}

// clearDialogState очищает состояние диалога для пользователя
func (b *Bot) clearDialogState(userID int64) {
	delete(b.dialogStates, userID)
}

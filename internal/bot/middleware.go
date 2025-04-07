package bot

import (
	"context" // Import errors for checking
	"log/slog"

	// Import apperrors
	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Middleware to log incoming updates
func (b *Bot) logMiddleware(next gobot.HandlerFunc) gobot.HandlerFunc {
	return func(ctx context.Context, bot *gobot.Bot, update *models.Update) {
		// Use DebugContext for consistency
		b.logger.DebugContext(ctx, "Processing update", slog.Int64("update_id", update.ID))
		next(ctx, bot, update)
	}
}

// Middleware to get or create user and put it in context
func (b *Bot) userMiddleware(next gobot.HandlerFunc) gobot.HandlerFunc {
	return func(ctx context.Context, bot *gobot.Bot, update *models.Update) {
		var tgUser models.User
		var chatID int64 // Keep chatID for potential error reporting
		var languageCode string
		var isPremium bool

		processUser := false
		switch {
		case update.Message != nil && update.Message.From != nil:
			tgUser = *update.Message.From
			chatID = update.Message.Chat.ID
			languageCode = tgUser.LanguageCode
			isPremium = tgUser.IsPremium
			processUser = true
		case update.CallbackQuery != nil:
			tgUser = update.CallbackQuery.From
			languageCode = tgUser.LanguageCode
			isPremium = tgUser.IsPremium
			processUser = true

			if update.CallbackQuery.Message.Message != nil {
				chatID = update.CallbackQuery.Message.Message.Chat.ID
			} else {
				// Use WarnContext
				b.logger.WarnContext(ctx, "ChatID unavailable in CallbackQuery message", slog.Int64("update_id", update.ID), slog.String("callback_query_id", update.CallbackQuery.ID))
				// Cannot send error to user if chatID is unknown
			}
		case update.PreCheckoutQuery != nil: // Process user for pre-checkout too
			tgUser = *update.PreCheckoutQuery.From
			languageCode = tgUser.LanguageCode
			isPremium = tgUser.IsPremium
			processUser = true
		default:
			// No user info in this update type (e.g., ChosenInlineResult), pass through
			b.logger.DebugContext(ctx, "Skipping user middleware for update type", slog.Int64("update_id", update.ID))
			next(ctx, bot, update)
			return
		}

		if !processUser || tgUser.ID == 0 || tgUser.IsBot {
			b.logger.DebugContext(ctx, "Skipping user middleware (no user/bot)", slog.Int64("update_id", update.ID), slog.Int64("tg_user_id", tgUser.ID), slog.Bool("is_bot", tgUser.IsBot))
			next(ctx, bot, update) // Skip if no user to process or it's a bot
			return
		}

		dbUser, err := b.userService.GetOrCreate(ctx,
			tgUser.ID,
			tgUser.IsBot,
			tgUser.FirstName,
			tgUser.LastName,
			tgUser.Username,
			languageCode,
			isPremium,
		)
		if err != nil {
			// Log error with context
			b.logger.ErrorContext(ctx, "Failed to get or create user in middleware", slog.Int64("telegram_id", tgUser.ID), slog.Any("error", err))
			if chatID != 0 {
				// Use helper to send internal error message
				b.sendInternalError(ctx, bot, chatID)
			}
			// Do not call next handler if user processing failed critically
			return
		}

		// Put user in context and call next handler
		newCtx := SetUserInContext(ctx, dbUser)
		b.logger.DebugContext(newCtx, "User added to context", slog.String("user_id", dbUser.ID.Hex()), slog.Int64("tg_user_id", dbUser.TelegramID))
		next(newCtx, bot, update)
	}
}

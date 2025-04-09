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

// adminRequired - middleware для проверки прав администратора
func (b *Bot) adminRequired(next gobot.HandlerFunc) gobot.HandlerFunc {
	return func(ctx context.Context, bot *gobot.Bot, update *models.Update) {
		user := UserFromContext(ctx)
		if user == nil {
			// Попытка отправить сообщение об ошибке, если возможно
			if update.Message != nil {
				b.sendUserError(ctx, bot, update.Message.Chat.ID, "Ошибка: не удалось получить информацию о пользователе.")
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, err := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Ошибка: не удалось получить информацию о пользователе.",
					ShowAlert:       true, // Показываем алерт, т.к. это ошибка
				})
				if err != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (user nil)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", err))
				}
			}
			return
		}

		isAdmin, err := b.userService.IsAdmin(ctx, user.ID)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to check admin status in middleware", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
			if update.Message != nil {
				b.sendInternalError(ctx, bot, update.Message.Chat.ID)
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, errAns := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Внутренняя ошибка сервера.",
					ShowAlert:       true,
				})
				if errAns != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (check error)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", errAns))
				}
			}
			return
		}

		if !isAdmin {
			b.logger.WarnContext(ctx, "Admin access denied", slog.String("user_id", user.ID.Hex()))
			if update.Message != nil {
				b.sendUserMessage(ctx, bot, update.Message.Chat.ID, "⛔ Доступ запрещен. Эта команда доступна только администраторам.")
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, errAns := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "⛔ Доступ запрещен.",
					ShowAlert:       true, // Показываем алерт об отказе
				})
				if errAns != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (access denied)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", errAns))
				}
			}
			return // Stop processing
		}

		// Если админ, передаем управление следующему обработчику
		next(ctx, bot, update)
	}
}

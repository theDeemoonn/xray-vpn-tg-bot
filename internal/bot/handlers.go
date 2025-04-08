package bot

import (
	"context"
	"log/slog"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// defaultHandler handles any message that doesn't match other handlers.
// It needs to be a method of *Bot to be used in WithDefaultHandler.
func (b *Bot) defaultHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	// Debug logging for update type
	updateType := "unknown"
	if update.Message != nil {
		updateType = "message"
	} else if update.CallbackQuery != nil {
		updateType = "callback_query"
	} else if update.PreCheckoutQuery != nil {
		updateType = "pre_checkout_query"
	} else if update.ShippingQuery != nil {
		updateType = "shipping_query"
	} else if update.ChannelPost != nil {
		updateType = "channel_post"
	} else if update.EditedMessage != nil {
		updateType = "edited_message"
	}
	b.logger.DebugContext(ctx, "Received update in defaultHandler", slog.String("update_type", updateType))

	if update.Message == nil {
		// Non-message updates: check if we have other important updates
		if update.PreCheckoutQuery != nil {
			b.logger.InfoContext(ctx, "PreCheckoutQuery received, passing to handler", slog.String("query_id", update.PreCheckoutQuery.ID))
			b.preCheckoutQueryHandler(ctx, bot, update)
			return
		}
		// Ignore other non-message updates like callback queries, etc., as they are handled elsewhere
		b.logger.DebugContext(ctx, "Ignoring non-message update in defaultHandler", slog.String("update_type", updateType))
		return
	}

	// Handle SuccessfulPayment message
	if update.Message.SuccessfulPayment != nil {
		b.logger.InfoContext(ctx, "SuccessfulPayment received in message, passing to handler", slog.String("charge_id", update.Message.SuccessfulPayment.TelegramPaymentChargeID))
		b.successfulPaymentHandler(ctx, bot, update)
		return
	}

	// --- Handle regular text messages --- //
	user := UserFromContext(ctx)
	userIDHex := "unknown"
	if user != nil {
		userIDHex = user.ID.Hex()
	}
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	// Check if it's admin input for server configuration
	if user != nil {
		isAdmin, _ := b.userService.IsAdmin(ctx, user.ID)
		if isAdmin {
			// Check if admin is in the middle of a dialog
			state := b.getDialogStateString(chatID, "state")
			if state != "" {
				b.logger.InfoContext(ctx, "Admin text message, passing to dialog handler",
					slog.String("user_id", userIDHex), slog.String("text", text), slog.String("dialog_state", state))
				b.handleAdminDialogInput(ctx, bot, update) // Use a general admin dialog handler
				return
			}
		}
	}

	// Handle unknown commands/text
	b.logger.InfoContext(ctx, "Received unhandled message",
		slog.Int64("chat_id", chatID),
		slog.String("user_id", userIDHex),
		slog.String("text", text))

	// Send a helpful message with the correct keyboard
	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Извините, я не понял вашу команду. Пожалуйста, используйте кнопки меню или команду /start.",
		ReplyMarkup: b.getUserKeyboard(ctx, user), // Assuming getUserKeyboard is available (moved to keyboards.go)
	}
	_, _ = bot.SendMessage(ctx, params)
}

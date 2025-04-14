package bot

import (
	"context"
	"errors"
	"log/slog"

	"xray-vpn-tg-bot/internal/apperrors"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// --- Payment Handlers --- //

// preCheckoutQueryHandler handles the query sent by Telegram before finalizing a payment.
func (b *Bot) preCheckoutQueryHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	query := update.PreCheckoutQuery
	if query == nil {
		b.logger.WarnContext(ctx, "Received update without PreCheckoutQuery in preCheckoutQueryHandler")
		return
	}

	b.logger.InfoContext(ctx, "Received PreCheckoutQuery",
		slog.String("query_id", query.ID),
		slog.String("payload", query.InvoicePayload),
		slog.Int("total_amount", query.TotalAmount),
		slog.String("currency", query.Currency))

	// Use the payload (which is our internal payment ID) to confirm with the service.
	// The service will check if the payment exists, is pending, and the plan is still valid.
	_, err := b.paymentService.ConfirmPreCheckout(ctx, query.InvoicePayload)

	// --- All checks passed, answer positively ---
	answerParams := &gobot.AnswerPreCheckoutQueryParams{PreCheckoutQueryID: query.ID}
	if err != nil {
		// Log the error (service layer should have logged details)
		b.logger.WarnContext(ctx, "PreCheckoutQuery rejected by service", slog.String("query_id", query.ID), slog.String("payload", query.InvoicePayload), slog.Any("error", err))

		// Provide a user-friendly error message if possible
		var appErr *apperrors.Error
		userMessage := "Не удалось подтвердить платеж. Попробуйте снова или выберите другой план."
		if errors.As(err, &appErr) {
			// Use the message from the app error if available
			userMessage = appErr.Message
		}
		answerParams.OK = false
		answerParams.ErrorMessage = userMessage
	} else {
		answerParams.OK = true
		b.logger.InfoContext(ctx, "PreCheckoutQuery approved by service", slog.String("query_id", query.ID), slog.String("payload", query.InvoicePayload))
	}

	// Answer the query
	_, answerErr := bot.AnswerPreCheckoutQuery(ctx, answerParams)
	if answerErr != nil {
		b.logger.ErrorContext(ctx, "Failed to answer PreCheckoutQuery", slog.String("query_id", query.ID), slog.Any("error", answerErr))
		// Log error, but maybe don't send another message to user here?
		return
	}
}

// successfulPaymentHandler handles the message about a successful payment.
func (b *Bot) successfulPaymentHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	sp := update.Message.SuccessfulPayment
	if sp == nil {
		b.logger.ErrorContext(ctx, "SuccessfulPayment data missing in handler")
		return
	}

	user := UserFromContext(ctx)
	if user == nil {
		b.logger.ErrorContext(ctx, "User context missing in successfulPaymentHandler", slog.Int64("chat_id", update.Message.Chat.ID))
		// Cannot easily send error message here as we don't have chatID easily without user
		return
	}
	chatID := update.Message.Chat.ID
	paymentInfo := sp
	payload := paymentInfo.InvoicePayload
	providerChargeID := paymentInfo.TelegramPaymentChargeID

	b.logger.InfoContext(ctx, "Received SuccessfulPayment",
		slog.String("user_id", user.ID.Hex()),
		slog.Int64("chat_id", chatID),
		slog.String("payload", payload),
		slog.String("provider_charge_id", providerChargeID),
		slog.String("currency", paymentInfo.Currency),
		slog.Int("total_amount", paymentInfo.TotalAmount))

	// Process the payment via the service (update DB, activate subscription)
	err := b.paymentService.ProcessSuccessfulPayment(ctx, payload, providerChargeID)

	if err != nil {
		// Check for conflict error (already processed)
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeConflict {
			// Log as warning, don't send error to user again
			b.logger.WarnContext(ctx, "Payment already processed (conflict detected)", slog.String("payload", payload))
			return
		}

		// Log other errors (service layer should have logged details)
		b.logger.ErrorContext(ctx, "Failed to process successful payment", slog.String("payload", payload), slog.Any("error", err))
		// Send error message to user
		b.sendUserError(ctx, bot, chatID, "Произошла ошибка при активации вашей подписки после оплаты. Пожалуйста, свяжитесь с поддержкой, указав детали платежа.")
		return
	}

	// Send success message to the user
	b.logger.InfoContext(ctx, "Payment processed and subscription activation triggered successfully", slog.String("payload", payload))
	// Modify the success message to include a warning about potential reconfiguration need and instructions
	successMsg := "✅ Оплата прошла успешно! Ваша подписка активирована (или продлена). " +
		"Проверьте раздел \"🚀 Мои подписки\"."
	// Add a note about potential server reconfiguration need
	successMsg += "\n\n⚠️ *Важно:* Если ваша подписка была продлена, возможно, потребуется заново выбрать сервер в разделе \"🚀 Мои подписки\", даже если он уже был настроен."
	// Add a suggestion to check instructions with a command link
	successMsg += "\n\nℹ️ Не знаете, как подключиться? Посмотрите [📱 Инструкции](/instrukcii), чтобы узнать, какие приложения использовать."

	b.sendUserMessage(ctx, bot, chatID, successMsg)
}

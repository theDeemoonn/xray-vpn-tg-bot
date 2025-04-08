package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// startHandler handles the /start command
func (b *Bot) startHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		if update.Message != nil {
			b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		}
		return
	}

	chatID := update.Message.Chat.ID
	tgUser := update.Message.From
	if tgUser == nil {
		b.logger.ErrorContext(ctx, "Nil From user in startHandler", slog.Int64("chat_id", chatID))
		b.sendInternalError(ctx, bot, chatID)
		return
	}
	b.logger.InfoContext(ctx, "Handling /start command", slog.Int64("chat_id", chatID), slog.String("user_db_id", user.ID.Hex()))

	// Проверяем, является ли пользователь администратором по ID из конфигурации
	// и автоматически назначаем администратором при первом запуске
	if b.cfg.Telegram.AdminID != 0 && user.TelegramID == b.cfg.Telegram.AdminID {
		// Проверяем текущий статус администратора
		isAdmin, err := b.userService.IsAdmin(ctx, user.ID)
		if err == nil && !isAdmin {
			// Если пользователь с ID из конфигурации еще не администратор,
			// то назначаем его администратором
			b.logger.InfoContext(ctx, "Auto-assigning admin status to configured admin ID",
				slog.Int64("telegram_id", user.TelegramID))

			// Устанавливаем статус администратора
			err = b.userService.SetAdmin(ctx, user.TelegramID, true)
			if err != nil {
				b.logger.ErrorContext(ctx, "Failed to auto-assign admin status",
					slog.Int64("telegram_id", user.TelegramID),
					slog.Any("error", err))
			}
		}
	}

	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        fmt.Sprintf("Добро пожаловать, %s! 👋\n\nЯ ваш помощник для управления VPN-подписками.\nИспользуйте меню ниже для навигации.", tgUser.FirstName),
		ReplyMarkup: b.getUserKeyboard(ctx, user),
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send start message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// buySubscriptionHandler shows available plans
func (b *Bot) buySubscriptionHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'Buy Subscription'", slog.String("user_id", user.ID.Hex()))

	plans, err := b.planService.GetActivePlans(ctx)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get active plans for user", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
		b.sendInternalError(ctx, bot, update.Message.Chat.ID)
		return
	}

	if len(plans) == 0 {
		b.sendUserMessage(ctx, bot, update.Message.Chat.ID, "К сожалению, доступных планов для покупки сейчас нет.")
		return
	}

	plansKeyboard := createPlansKeyboard(plans)

	var msgText strings.Builder
	msgText.WriteString("📋 Выберите тарифный план:\n\n")

	for _, plan := range plans {
		msgText.WriteString(fmt.Sprintf("*%s*\n", escapeMarkdownV2(plan.Name)))
		msgText.WriteString(fmt.Sprintf("💰 Цена: %s\n", escapeMarkdownV2(fmt.Sprintf("%.2f %s", plan.Price, plan.Currency))))
		msgText.WriteString(fmt.Sprintf("⏱ Длительность: %s\n", escapeMarkdownV2(plan.DurationString())))

		if plan.TrafficGB > 0 {
			msgText.WriteString(fmt.Sprintf("📊 Трафик: %s\n", escapeMarkdownV2(fmt.Sprintf("%d GB", plan.TrafficGB))))
		} else {
			msgText.WriteString("📊 Трафик: Безлимитный\n")
		}

		msgText.WriteString("\n")
	}

	params := &gobot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        msgText.String(),
		ReplyMarkup: plansKeyboard,
		ParseMode:   "MarkdownV2",
	}

	_, err = bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send plans message", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
	}
}

// mySubscriptionsHandler displays the user's active subscriptions
func (b *Bot) mySubscriptionsHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'My Subscriptions'", slog.String("user_id", user.ID.Hex()))

	// Get all active subscriptions, for simplicity we'll display the first one if any
	subs, err := b.subscriptionService.GetActiveSubscriptionForUser(ctx, user.ID)
	if err != nil {
		// If it's not specifically ErrNotFound, it's an internal error
		if !errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			b.logger.ErrorContext(ctx, "Failed to get active subscription for user", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
			b.sendInternalError(ctx, bot, update.Message.Chat.ID)
			return
		}
	}

	if len(subs) == 0 {
		b.sendUserMessage(ctx, bot, update.Message.Chat.ID, "У вас нет активных подписок. 🛒")
		return
	}

	// Для простоты берем первую подписку
	subDetails := subs[0]
	sub := subDetails.Subscription

	var msgText string
	var replyMarkup models.ReplyMarkup

	// Используем имя плана из деталей подписки
	planName := subDetails.PlanName

	// Format expiry date (adjust locale/format as needed)
	loc, _ := time.LoadLocation("Europe/Moscow") // Example: Moscow timezone
	expiryStr := sub.ExpiresAt.In(loc).Format("02.01.2006 15:04 MST")

	// Format traffic usage/limit (convert bytes to GB)
	trafficLimitStr := "Безлимитно ∞"
	if sub.TrafficLimit > 0 {
		trafficLimitStr = fmt.Sprintf("%.2f GB", float64(sub.TrafficLimit)/(1024*1024*1024))
	}
	trafficUsedStr := fmt.Sprintf("%.2f GB", float64(sub.TrafficUsed)/(1024*1024*1024))

	// Build message text without manual MarkdownV2 escapes inside
	msgTextFormat := `✨ *Ваша активная подписка:*` +
		`
План: *%s*` +
		`
Истекает: *%s*` +
		`

Трафик (исп./лимит): *%s* / *%s*` +
		`
Статус: *%s*`

	// Add button based on whether server is configured
	if !sub.ServerID.IsZero() {
		// Server is configured - show Get Config button
		inlineKeyboard := [][]models.InlineKeyboardButton{{ // Correct structure
			{
				Text:         "🔗 Получить конфигурацию",
				CallbackData: callbackActionGetConfig + sub.ID.Hex(),
			},
		}}
		replyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: inlineKeyboard}
		msgTextFormat += "\n\nНажмите кнопку ниже, чтобы получить конфигурацию."
	} else {
		// Server is NOT configured - show Configure Server button
		inlineKeyboard := [][]models.InlineKeyboardButton{{ // Correct structure
			{
				Text:         "⚙️ Настроить сервер",
				CallbackData: callbackActionSelectServer + sub.ID.Hex(), // Use sub ID
			},
		}}
		replyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: inlineKeyboard}
		msgTextFormat += "\n\n_Подписка активна, но еще не настроена. Нажмите кнопку ниже, чтобы выбрать сервер._"
	}

	msgText = fmt.Sprintf(msgTextFormat,
		planName,                    // Assuming planName is already escaped or safe
		expiryStr,                   // Assuming expiryStr is safe
		trafficUsedStr,              // Assuming trafficUsedStr is safe
		trafficLimitStr,             // Assuming trafficLimitStr is safe
		translateStatus(sub.Status), // Assuming translateStatus is safe - lives in helpers.go
	)

	// Escape the final message text for MarkdownV2
	escapedMsgText := escapeMarkdownV2(msgText) // Assuming escapeMarkdownV2 lives in helpers.go

	params := &gobot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        escapedMsgText,
		ParseMode:   "MarkdownV2",
		ReplyMarkup: replyMarkup,
	}
	_, _ = bot.SendMessage(ctx, params)
}

// referralHandler displays referral information.
func (b *Bot) referralHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'Referral Program'", slog.String("user_id", user.ID.Hex()))
	msgText := fmt.Sprintf("Ваш реферальный код: `%s`\n\nРаздел 'Реферальная программа' в разработке.", user.ReferralCode)
	// Escape for MarkdownV2
	escapedMsgText := escapeMarkdownV2(msgText)
	params := &gobot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      escapedMsgText,
		ParseMode: "MarkdownV2",
	}
	_, _ = bot.SendMessage(ctx, params)
}

// faqHandler displays the FAQ category selection.
func (b *Bot) faqHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'FAQ' button", slog.String("user_id", user.ID.Hex()))

	// Show FAQ categories by sending a new message
	b.showFAQCategories(ctx, bot, update.Message.Chat.ID, 0) // 0 for new message
}

// instructionHandler handles the "Instructions" button.
func (b *Bot) instructionHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'Instructions' button", slog.String("user_id", user.ID.Hex()))

	// Directly show platform selection
	b.showInstructionPlatforms(ctx, bot, update.Message.Chat.ID, 0) // 0 for new message
}

// supportHandler handles the "Support" button.
func (b *Bot) supportHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'Support' button", slog.String("user_id", user.ID.Hex()))

	adminUsername := b.cfg.Telegram.AdminUsername
	msgText := "Для связи с поддержкой напишите администратору."
	if adminUsername != "" {
		msgText = fmt.Sprintf("Для связи с поддержкой напишите администратору: @%s", adminUsername)
	} else if b.cfg.Telegram.AdminID != 0 {
		// Fallback if username is not set but ID is
		msgText = fmt.Sprintf("Для связи с поддержкой напишите администратору ID: %d. К сожалению, имя пользователя не указано.", b.cfg.Telegram.AdminID)
	}

	params := &gobot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      escapeMarkdownV2(msgText), // Escape potential special chars
		ParseMode: "MarkdownV2",
	}
	_, _ = bot.SendMessage(ctx, params)
}

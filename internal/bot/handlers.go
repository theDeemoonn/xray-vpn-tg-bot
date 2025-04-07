package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	qrcode "github.com/skip2/go-qrcode"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// --- Command Handlers --- //

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

	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        fmt.Sprintf("Добро пожаловать, %s! 👋\n\nЯ ваш помощник для управления VPN-подписками.\nИспользуйте меню ниже для навигации.", tgUser.FirstName),
		ReplyMarkup: b.mainMenuKeyboard(),
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send start message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// --- Text Handlers (mapped to keyboard buttons) ---

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
	sub, err := b.subscriptionService.GetUserActiveSubscription(ctx, user.ID)
	if err != nil {
		// If it's not specifically ErrNotFound, it's an internal error
		if !errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			b.logger.ErrorContext(ctx, "Failed to get active subscription for user", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
			b.sendInternalError(ctx, bot, update.Message.Chat.ID)
			return
		}
	}
	if sub == nil {
		b.sendUserMessage(ctx, bot, update.Message.Chat.ID, "У вас нет активных подписок. 🛒")
		return
	}

	var msgText string
	var replyMarkup models.ReplyMarkup

	// Fetch plan details to show name
	plan, planErr := b.planService.GetPlanByID(ctx, sub.PlanID)
	planName := "Неизвестный план"
	if planErr == nil && plan != nil {
		planName = plan.Name
	} else {
		b.logger.WarnContext(ctx, "Failed to get plan details for active subscription display", slog.String("sub_id", sub.ID.Hex()), slog.String("plan_id", sub.PlanID.Hex()), slog.Any("error", planErr))
	}

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

// faqHandler displays FAQ information.
func (b *Bot) faqHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}
	b.logger.InfoContext(ctx, "Handling 'FAQ & Support'", slog.String("user_id", user.ID.Hex()))
	params := &gobot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Раздел 'FAQ & Поддержка' в разработке.",
	}
	_, _ = bot.SendMessage(ctx, params)
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
		msgText = fmt.Sprintf("Для связи с поддержкой напишите администратору (ID: %d). К сожалению, имя пользователя не указано.", b.cfg.Telegram.AdminID)
	}

	params := &gobot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      escapeMarkdownV2(msgText), // Escape potential special chars
		ParseMode: "MarkdownV2",
	}
	_, _ = bot.SendMessage(ctx, params)
}

// --- Callback Query Handlers --- //

// handlePlanSelectionCallback handles the selection of a plan via inline keyboard
func (b *Bot) handlePlanSelectionCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		if update.CallbackQuery != nil {
			// Answer callback query first
			_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Ошибка: пользователь не найден",
				ShowAlert:       true,
			})
		}
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	callbackData := update.CallbackQuery.Data
	planIDHex := strings.TrimPrefix(callbackData, callbackActionSelectPlan)

	b.logger.InfoContext(ctx, "Handling plan selection callback",
		slog.String("user_id", user.ID.Hex()),
		slog.String("plan_id_hex", planIDHex))

	planID, err := primitive.ObjectIDFromHex(planIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Invalid plan ID in callback data", slog.String("data", callbackData), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Неверный ID тарифного плана.")
		// Answer callback query
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Get plan details to show in invoice
	plan, err := b.planService.GetPlanByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			b.logger.WarnContext(ctx, "Plan selected via callback not found", slog.String("plan_id", planIDHex))
			b.sendUserError(ctx, bot, chatID, "Выбранный тарифный план не найден или больше не доступен.")
		} else {
			b.logger.ErrorContext(ctx, "Failed to get plan details for invoice", slog.String("plan_id", planIDHex), slog.Any("error", err))
			b.sendInternalError(ctx, bot, chatID)
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Create a pending payment record locally and get its ID as payload
	payload, err := b.paymentService.CreatePendingPayment(ctx, user.ID, plan.ID)
	if err != nil {
		// Service layer logged the error
		b.sendInternalError(ctx, bot, chatID)
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Send invoice using Telegram Payments
	prices := []models.LabeledPrice{{
		Label:  plan.Name,
		Amount: int(plan.Price * 100), // Amount in smallest currency unit (kopecks for RUB)
	}}

	params := &gobot.SendInvoiceParams{
		ChatID:                    chatID,
		Title:                     plan.Name,                                                // Invoice title
		Description:               fmt.Sprintf("Подписка VPN на %s", plan.DurationString()), // Invoice description
		Payload:                   payload,                                                  // Our internal payment ID
		ProviderToken:             b.cfg.Telegram.ProviderToken,                             // Use ProviderToken from config
		Currency:                  strings.ToUpper(plan.Currency),                           // Currency code (e.g., "RUB")
		Prices:                    prices,
		NeedName:                  false, // Adjust based on provider requirements
		NeedPhoneNumber:           false,
		NeedEmail:                 false,
		NeedShippingAddress:       false,
		SendPhoneNumberToProvider: false,
		SendEmailToProvider:       false,
		IsFlexible:                false, // Set to true if prices depend on shipping
		ReplyMarkup:               &models.InlineKeyboardMarkup{ /* Optional inline keyboard for invoice */ },
		// ProviderData:          "{}", // Optional JSON object for provider
		// PhotoURL:              "", // Optional photo URL
		// PhotoSize:             0,
		// PhotoWidth:            0,
		// PhotoHeight:           0,
		// ProtectContent:        false,
		// MessageThreadID:       0,
		// StartParameter:        "",
	}

	_, err = bot.SendInvoice(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send invoice", slog.String("user_id", user.ID.Hex()), slog.String("plan_id", plan.ID.Hex()), slog.Any("error", err))
		b.sendInternalError(ctx, bot, chatID)
	}

	// Answer callback query (empty text is fine)
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleGetConfigCallback handles the "Get Configuration" button press
func (b *Bot) handleGetConfigCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		if update.CallbackQuery != nil {
			_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		}
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	callbackData := update.CallbackQuery.Data
	subIDHex := strings.TrimPrefix(callbackData, callbackActionGetConfig)

	b.logger.InfoContext(ctx, "Handling get config callback", slog.String("user_id", user.ID.Hex()), slog.String("sub_id_hex", subIDHex))

	subID, err := primitive.ObjectIDFromHex(subIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Invalid subscription ID in callback data", slog.String("data", callbackData), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Неверный ID подписки.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Get subscription details (verify ownership and server config)
	sub, err := b.subscriptionService.GetSubscriptionByID(ctx, subID)
	if err != nil {
		if errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			b.logger.WarnContext(ctx, "Subscription not found for get_config callback", slog.String("sub_id", subIDHex))
			b.sendUserError(ctx, bot, chatID, "Подписка не найдена.")
		} else {
			b.logger.ErrorContext(ctx, "Failed to get subscription for get_config", slog.String("sub_id", subIDHex), slog.Any("error", err))
			b.sendInternalError(ctx, bot, chatID)
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Verify ownership
	if sub.UserID != user.ID {
		b.logger.WarnContext(ctx, "User attempted to get config for subscription belonging to another user", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", sub.ID.Hex()), slog.String("sub_owner_id", sub.UserID.Hex()))
		b.sendUserError(ctx, bot, chatID, "Ошибка доступа к подписке.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Check if server is configured
	if sub.ServerID.IsZero() {
		b.logger.WarnContext(ctx, "User attempted to get config for unconfigured subscription", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", sub.ID.Hex()))
		b.sendUserWarning(ctx, bot, chatID, "Сервер для этой подписки еще не настроен. Пожалуйста, выберите сервер.") // Changed to warning
		// Optionally, resend the select server keyboard?
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Generate the config link
	configLink, err := b.subscriptionService.GetConfigLink(ctx, sub)
	if err != nil {
		// Check for specific user-facing errors from the service
		var appErr *apperrors.Error
		if errors.As(err, &appErr) {
			switch appErr.Code {
			case apperrors.ErrCodeNotFound: // E.g., server or inbound not found on X-UI side
				b.logger.WarnContext(ctx, "Config link generation failed (Not Found)", slog.String("sub_id", sub.ID.Hex()), slog.String("error", appErr.Error()))
				b.sendUserError(ctx, bot, chatID, fmt.Sprintf("Не удалось сгенерировать ссылку: %s", appErr.Message))
			case apperrors.ErrCodeValidation: // E.g., unsupported protocol
				b.logger.WarnContext(ctx, "Config link generation failed (Validation)", slog.String("sub_id", sub.ID.Hex()), slog.String("error", appErr.Error()))
				b.sendUserError(ctx, bot, chatID, fmt.Sprintf("Ошибка конфигурации: %s", appErr.Message))
			default:
				b.logger.ErrorContext(ctx, "Config link generation failed (Internal/Unknown)", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
				b.sendInternalError(ctx, bot, chatID)
			}
		} else {
			// Unexpected error type
			b.logger.ErrorContext(ctx, "Unexpected error type during config link generation", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
			b.sendInternalError(ctx, bot, chatID)
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// --- Generate QR Code --- //
	qrCodeContent := configLink
	qrCode, err := qrcode.Encode(qrCodeContent, qrcode.Medium, 256)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to generate QR code", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
		// Send link without QR code
		msg := fmt.Sprintf("Ваша ссылка для подключения:\n`%s`\n\n(Не удалось сгенерировать QR-код)", escapeMarkdownV2(configLink))
		b.sendUserMessage(ctx, bot, chatID, msg)
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Ссылка отправлена"})
		return
	}

	// --- Send QR Code and Link --- //
	msgText := fmt.Sprintf("Ваша ссылка для подключения:\n`%s`\n\nОтсканируйте QR-код или скопируйте ссылку.", escapeMarkdownV2(configLink))

	// Use SendPhoto to send the QR code image
	photoParams := &gobot.SendPhotoParams{
		ChatID:    chatID,
		Photo:     &models.InputFileUpload{Filename: "config_qr.png", Data: bytes.NewReader(qrCode)},
		Caption:   msgText,
		ParseMode: "MarkdownV2", // Caption needs parsing
	}

	_, err = bot.SendPhoto(ctx, photoParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send QR code photo", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
		// Fallback to sending text only
		b.sendUserMessage(ctx, bot, chatID, msgText)
	}

	// Answer the callback query
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Конфигурация отправлена"})
}

// handleSelectServerCallback handles the "Configure Server" button press
func (b *Bot) handleSelectServerCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		if update.CallbackQuery != nil {
			_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		}
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	callbackData := update.CallbackQuery.Data
	subIDHex := strings.TrimPrefix(callbackData, callbackActionSelectServer)

	b.logger.InfoContext(ctx, "Handling select server callback", slog.String("user_id", user.ID.Hex()), slog.String("sub_id_hex", subIDHex))

	subID, err := primitive.ObjectIDFromHex(subIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Invalid subscription ID in callback data", slog.String("data", callbackData), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Неверный ID подписки.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Verify subscription ownership and status
	sub, err := b.subscriptionService.GetSubscriptionByID(ctx, subID)
	if err != nil {
		if errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			b.logger.WarnContext(ctx, "Subscription not found for select_server callback", slog.String("sub_id", subIDHex))
			b.sendUserError(ctx, bot, chatID, "Подписка не найдена.")
		} else {
			b.logger.ErrorContext(ctx, "Failed to get subscription for select_server", slog.String("sub_id", subIDHex), slog.Any("error", err))
			b.sendInternalError(ctx, bot, chatID)
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}
	if sub.UserID != user.ID {
		b.logger.WarnContext(ctx, "User attempted to select server for subscription belonging to another user", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", sub.ID.Hex()), slog.String("sub_owner_id", sub.UserID.Hex()))
		b.sendUserError(ctx, bot, chatID, "Ошибка доступа к подписке.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Check if already configured
	if !sub.ServerID.IsZero() {
		b.logger.InfoContext(ctx, "User attempted to reconfigure already configured subscription", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()))
		// Inform user and maybe offer to reconfigure or get config?
		b.sendUserWarning(ctx, bot, chatID, "Эта подписка уже настроена. Вы можете получить конфигурацию в разделе 'Мои подписки'.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Get available servers - Use the correct method name
	servers, err := b.serverService.ListEnabledServers(ctx)
	if err != nil {
		// Logged in service layer
		b.sendInternalError(ctx, bot, chatID)
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	if len(servers) == 0 {
		b.logger.WarnContext(ctx, "No available servers found for selection", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", sub.ID.Hex()))
		b.sendUserMessage(ctx, bot, chatID, "К сожалению, сейчас нет доступных серверов для подключения. Попробуйте позже.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Build keyboard with servers - Use correct function name and argument type
	serverKeyboard := createServerSelectionKeyboard(servers, sub.ID)

	// Edit the original message to show the server selection
	// Add check for nil Message
	if update.CallbackQuery.Message.Message == nil {
		b.logger.ErrorContext(ctx, "Cannot edit message for server selection: original message is nil/inaccessible", slog.String("callback_query_id", update.CallbackQuery.ID))
		b.sendInternalError(ctx, bot, chatID)
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}
	editParams := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   update.CallbackQuery.Message.Message.MessageThreadID, // Fix after
		Text:        "Выберите сервер для подключения:",
		ReplyMarkup: serverKeyboard,
	}
	_, err = bot.EditMessageText(ctx, editParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for server selection", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
	}

	// Answer callback query
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleServerSelectionCallback handles the selection of a server via inline keyboard
func (b *Bot) handleServerSelectionCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		if update.CallbackQuery != nil {
			_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		}
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	callbackData := update.CallbackQuery.Data

	// Use the correct constant
	parts := strings.Split(strings.TrimPrefix(callbackData, callbackActionConfirmServer), "_")
	if len(parts) != 2 {
		b.logger.ErrorContext(ctx, "Invalid server confirmation callback data format", slog.String("data", callbackData))
		b.sendUserError(ctx, bot, chatID, "Некорректные данные выбора сервера.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}
	subIDHex := parts[0]
	serverIDHex := parts[1]

	b.logger.InfoContext(ctx, "Handling server confirmation callback",
		slog.String("user_id", user.ID.Hex()),
		slog.String("sub_id_hex", subIDHex),
		slog.String("server_id_hex", serverIDHex))

	subID, errSub := primitive.ObjectIDFromHex(subIDHex)
	serverID, errSrv := primitive.ObjectIDFromHex(serverIDHex)

	if errSub != nil || errSrv != nil {
		b.logger.ErrorContext(ctx, "Invalid ObjectID in server confirmation callback", slog.String("data", callbackData), slog.Any("errSub", errSub), slog.Any("errSrv", errSrv))
		b.sendUserError(ctx, bot, chatID, "Некорректные данные выбора сервера.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Call the subscription service to configure the server for the subscription
	err := b.subscriptionService.ConfigureSubscriptionServer(ctx, user.ID, subID, serverID)
	if err != nil {
		// Handle specific errors from service
		var appErr *apperrors.Error
		if errors.As(err, &appErr) {
			switch appErr.Code {
			case apperrors.ErrCodeNotFound: // Sub, User, Server, Plan not found
				b.logger.WarnContext(ctx, "Server configuration failed (Not Found)", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", subIDHex), slog.String("server_id", serverIDHex), slog.String("error", appErr.Error()))
				b.sendUserError(ctx, bot, chatID, fmt.Sprintf("Ошибка настройки: %s", appErr.Message))
			case apperrors.ErrCodeConflict: // Subscription already configured, server full, etc.
				b.logger.WarnContext(ctx, "Server configuration failed (Conflict)", slog.String("user_id", user.ID.Hex()), slog.String("sub_id", subIDHex), slog.String("server_id", serverIDHex), slog.String("error", appErr.Error()))
				b.sendUserWarning(ctx, bot, chatID, fmt.Sprintf("Не удалось настроить: %s", appErr.Message))
			default:
				// Internal error logged by service
				b.sendInternalError(ctx, bot, chatID)
			}
		} else {
			// Unexpected error type
			b.logger.ErrorContext(ctx, "Unexpected error type during server configuration", slog.String("sub_id", subIDHex), slog.String("server_id", serverIDHex), slog.Any("error", err))
			b.sendInternalError(ctx, bot, chatID)
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	configButton := [][]models.InlineKeyboardButton{{{
		Text:         "🔗 Получить конфигурацию",
		CallbackData: callbackActionGetConfig + subIDHex,
	}}}

	configMarkup := models.InlineKeyboardMarkup{InlineKeyboard: configButton}

	// Get server name for confirmation message
	server, srvErr := b.serverService.GetServer(ctx, serverIDHex)
	serverName := "выбранный сервер"
	if srvErr == nil && server != nil {
		serverName = server.Name
	} else {
		b.logger.WarnContext(ctx, "Failed to get server name for confirmation message", slog.String("server_id", serverIDHex), slog.Any("error", srvErr))
	}

	// Add check for nil Message
	// Проверяем наличие сообщения и используем configMarkup в EditMessageTextParams
	if update.CallbackQuery.Message.Message == nil {
		b.logger.ErrorContext(ctx, "Cannot edit message after server configuration: original message is nil/inaccessible", slog.String("callback_query_id", update.CallbackQuery.ID))
		// Send new message as fallback?
		b.sendUserMessage(ctx, bot, chatID, fmt.Sprintf("✅ Подписка успешно настроена на сервер '%s'!", escapeMarkdownV2(serverName))) // Simple fallback
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Сервер настроен!"})
		return
	}
	editParams := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   update.CallbackQuery.Message.Message.MessageThreadID, // Safe now
		Text:        fmt.Sprintf("✅ Подписка успешно настроена на сервер '%s'!\n\nНажмите кнопку ниже, чтобы получить конфигурацию.", escapeMarkdownV2(serverName)),
		ParseMode:   "MarkdownV2",
		ReplyMarkup: configMarkup,
	}
	_, err = bot.EditMessageText(ctx, editParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message after successful server configuration", slog.String("sub_id", subIDHex), slog.Any("error", err))
	}

	// Answer callback query
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Сервер настроен!"})
}

// --- FAQ & Instruction Callback Handlers --- //

// handleBackToFAQCallback handles the "Back to FAQ" button press (shows categories)
func (b *Bot) handleBackToFAQCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	b.logger.DebugContext(ctx, "Handling back to FAQ callback", slog.Int64("chat_id", chatID))
	b.showFAQCategories(ctx, bot, chatID, messageID)
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleBackToInstructionsCallback handles the "Back to Instructions" button press (shows platforms)
func (b *Bot) handleBackToInstructionsCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	b.logger.DebugContext(ctx, "Handling back to Instructions callback", slog.Int64("chat_id", chatID))
	b.showInstructionPlatforms(ctx, bot, chatID, messageID)
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleBackToHelpRootCallback handles the "Back" button press from category/platform lists
func (b *Bot) handleBackToHelpRootCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	b.logger.DebugContext(ctx, "Handling back to help root callback", slog.Int64("chat_id", chatID))

	params := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        "Выберите нужный раздел:",
		ReplyMarkup: createHelpRootKeyboard(),
	}
	_, err := bot.EditMessageText(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for help root", slog.Any("error", err))
	}
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleFAQCategoryCallback handles selection of an FAQ category.
func (b *Bot) handleFAQCategoryCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	callbackData := update.CallbackQuery.Data
	category := strings.TrimPrefix(callbackData, callbackPrefixFAQCategory)

	b.logger.InfoContext(ctx, "Handling FAQ category selection", slog.Int64("chat_id", chatID), slog.String("category", category))

	faqs, err := b.faqService.GetQuestionsByCategory(ctx, category)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get FAQs for category", slog.String("category", category), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить вопросы для этой категории.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	if len(faqs) == 0 {
		// Edit the message to indicate no questions, keep the back button
		params := &gobot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        fmt.Sprintf("В категории '%s' пока нет вопросов.", escapeMarkdownV2(category)),
			ReplyMarkup: createFAQCategoriesKeyboard([]string{}), // Pass empty slice to just get back button
			ParseMode:   "MarkdownV2",
		}
		_, editErr := bot.EditMessageText(ctx, params)
		if editErr != nil {
			b.logger.ErrorContext(ctx, "Failed to edit message for empty FAQ category", slog.String("category", category), slog.Any("error", editErr))
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	keyboard := createFAQQuestionsKeyboard(faqs, category)
	params := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        fmt.Sprintf("Выберите вопрос из категории '%s':", escapeMarkdownV2(category)),
		ReplyMarkup: keyboard,
		ParseMode:   "MarkdownV2",
	}

	_, err = bot.EditMessageText(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for FAQ questions", slog.String("category", category), slog.Any("error", err))
	}

	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleFAQQuestionCallback handles selection of a specific FAQ question.
func (b *Bot) handleFAQQuestionCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	callbackData := update.CallbackQuery.Data
	faqIDHex := strings.TrimPrefix(callbackData, callbackPrefixFAQQuestion)

	b.logger.InfoContext(ctx, "Handling FAQ question selection", slog.Int64("chat_id", chatID), slog.String("faq_id", faqIDHex))

	faqID, err := primitive.ObjectIDFromHex(faqIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Invalid FAQ ID in callback", slog.String("faq_id_hex", faqIDHex), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Неверный ID вопроса.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	faq, err := b.faqService.GetAnswer(ctx, faqID)
	if err != nil {
		// Handle not found specifically?
		b.logger.ErrorContext(ctx, "Failed to get FAQ answer", slog.String("faq_id", faqIDHex), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить ответ на вопрос.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Format the response (assuming MarkdownV2 is safe in Answer)
	msgText := fmt.Sprintf("*❓ %s*\n\n%s", escapeMarkdownV2(faq.Question), escapeMarkdownV2(faq.Answer))

	// Pass the category back to the keyboard function if needed for the back button logic
	// Assuming GetAnswer returns the full FAQ object including category
	keyboard := createFAQQuestionsKeyboard([]*domain.FAQ{faq}, faq.Category) // Re-use keyboard func, pass current faq and its category
	backButtonRow := createBackButtonRow(callbackPrefixBackToFAQ)            // Get the standard back button
	keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1] = backButtonRow  // Ensure the last row is the back button to categories

	params := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        msgText,
		ReplyMarkup: keyboard,
		ParseMode:   "MarkdownV2",
	}

	_, err = bot.EditMessageText(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for FAQ answer", slog.String("faq_id", faqIDHex), slog.Any("error", err))
	}

	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleInstructionPlatformCallback handles selection of an instruction platform.
func (b *Bot) handleInstructionPlatformCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	callbackData := update.CallbackQuery.Data
	platform := strings.TrimPrefix(callbackData, callbackPrefixInstructionPlatform)

	b.logger.InfoContext(ctx, "Handling instruction platform selection", slog.Int64("chat_id", chatID), slog.String("platform", platform))

	instructions, err := b.instructionService.GetInstructionsByPlatform(ctx, platform)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get instructions for platform", slog.String("platform", platform), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить инструкции для этой платформы.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	if len(instructions) == 0 {
		// Edit the message to indicate no instructions, keep the back button
		params := &gobot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        fmt.Sprintf("Для платформы '%s' пока нет инструкций.", escapeMarkdownV2(platform)),
			ReplyMarkup: createInstructionPlatformsKeyboard([]string{}), // Pass empty slice to just get back button
			ParseMode:   "MarkdownV2",
		}
		_, editErr := bot.EditMessageText(ctx, params)
		if editErr != nil {
			b.logger.ErrorContext(ctx, "Failed to edit message for empty instruction platform", slog.String("platform", platform), slog.Any("error", editErr))
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	keyboard := createInstructionsListKeyboard(instructions, platform)
	params := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        fmt.Sprintf("Выберите инструкцию для '%s':", escapeMarkdownV2(platform)),
		ReplyMarkup: keyboard,
		ParseMode:   "MarkdownV2",
	}

	_, err = bot.EditMessageText(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for instruction list", slog.String("platform", platform), slog.Any("error", err))
	}

	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleInstructionDetailsCallback handles selection of a specific instruction.
func (b *Bot) handleInstructionDetailsCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.MessageThreadID
	callbackData := update.CallbackQuery.Data
	instructionIDHex := strings.TrimPrefix(callbackData, callbackPrefixInstructionDetails)

	b.logger.InfoContext(ctx, "Handling instruction details selection", slog.Int64("chat_id", chatID), slog.String("instruction_id", instructionIDHex))

	instructionID, err := primitive.ObjectIDFromHex(instructionIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Invalid instruction ID in callback", slog.String("instruction_id_hex", instructionIDHex), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Неверный ID инструкции.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	instruction, err := b.instructionService.GetInstructionDetails(ctx, instructionID)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get instruction details", slog.String("instruction_id", instructionIDHex), slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить инструкцию.")
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Format the response (assuming MarkdownV2 is safe in Content)
	msgText := fmt.Sprintf("*📱 %s* (%s)\n\n%s", escapeMarkdownV2(instruction.Title), escapeMarkdownV2(instruction.Platform), escapeMarkdownV2(instruction.Content))

	// Pass the platform back to the keyboard function if needed for the back button logic
	keyboard := createInstructionsListKeyboard([]*domain.Instruction{instruction}, instruction.Platform) // Re-use keyboard func
	backButtonRow := createBackButtonRow(callbackPrefixBackToInstructions)                               // Get the standard back button
	keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1] = backButtonRow                              // Ensure last row is back button to platforms

	params := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        msgText,
		ReplyMarkup: keyboard,
		ParseMode:   "MarkdownV2",
		// DisableWebPagePreview: true, // Consider if Content might contain links
	}

	_, err = bot.EditMessageText(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to edit message for instruction details", slog.String("instruction_id", instructionIDHex), slog.Any("error", err))
	}

	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// --- Update Handlers (Default, PreCheckout, SuccessfulPayment) ---

// defaultHandler handles any message that doesn't match other handlers.
// It needs to be a method of *Bot to be used in WithDefaultHandler.
func (b *Bot) defaultHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	if update.Message == nil {
		// Ignore non-message updates in default handler (e.g., channel posts)
		return
	}

	// Handle PreCheckoutQuery if present
	if update.PreCheckoutQuery != nil {
		b.preCheckoutQueryHandler(ctx, bot, update)
		return
	}

	// Handle SuccessfulPayment if present
	if update.Message.SuccessfulPayment != nil {
		b.successfulPaymentHandler(ctx, bot, update)
		return
	}

	// Handle regular unhandled messages
	user := UserFromContext(ctx)
	userIDHex := "unknown"
	if user != nil {
		userIDHex = user.ID.Hex()
	}
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	b.logger.InfoContext(ctx, "Received unhandled message",
		slog.Int64("chat_id", chatID),
		slog.String("user_id", userIDHex),
		slog.String("text", text))

	// Send a helpful message
	b.sendUserMessage(ctx, bot, chatID, "Извините, я не понял вашу команду. Пожалуйста, используйте кнопки меню или команду /start.")
}

// --- Payment Handlers --- //

// preCheckoutQueryHandler handles the pre-checkout query from Telegram.
// This function can remain as it is, called by defaultHandler.
func (b *Bot) preCheckoutQueryHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	if update.PreCheckoutQuery == nil {
		b.logger.ErrorContext(ctx, "PreCheckoutQuery is nil in handler")
		return // Cannot proceed
	}

	queryID := update.PreCheckoutQuery.ID
	payload := update.PreCheckoutQuery.InvoicePayload
	tgUserID := update.PreCheckoutQuery.From.ID // Use Telegram User ID for logging

	b.logger.InfoContext(ctx, "Received PreCheckoutQuery",
		slog.String("query_id", queryID),
		slog.String("payload", payload),
		slog.Int64("tg_user_id", tgUserID))

	// Call payment service to confirm
	_, err := b.paymentService.ConfirmPreCheckout(ctx, payload)

	answerParams := &gobot.AnswerPreCheckoutQueryParams{PreCheckoutQueryID: queryID}
	if err != nil {
		// Log the error (service layer should have logged details)
		b.logger.WarnContext(ctx, "PreCheckoutQuery rejected", slog.String("query_id", queryID), slog.String("payload", payload), slog.Any("error", err))

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
		b.logger.InfoContext(ctx, "PreCheckoutQuery approved", slog.String("query_id", queryID), slog.String("payload", payload))
	}

	// Answer the query
	_, answerErr := bot.AnswerPreCheckoutQuery(ctx, answerParams)
	if answerErr != nil {
		b.logger.ErrorContext(ctx, "Failed to answer PreCheckoutQuery", slog.String("query_id", queryID), slog.Any("error", answerErr))
	}
}

// successfulPaymentHandler handles successful payment notifications.
// This function can remain as it is, called by defaultHandler.
func (b *Bot) successfulPaymentHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.SuccessfulPayment == nil {
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
	paymentInfo := update.Message.SuccessfulPayment
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
	b.sendUserMessage(ctx, bot, chatID, "✅ Оплата прошла успешно! Ваша подписка активирована (или продлена). Проверьте раздел 'Мои подписки'.")
}

// --- Вспомогательные функции для FAQ/Инструкций --- //

// showFAQCategories отправляет или редактирует сообщение со списком категорий FAQ
func (b *Bot) showFAQCategories(ctx context.Context, bot *gobot.Bot, chatID int64, messageID int) {
	categories, err := b.faqService.GetCategories(ctx)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get FAQ categories", slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить категории FAQ.")
		return
	}

	if len(categories) == 0 {
		b.sendUserMessage(ctx, bot, chatID, "Раздел FAQ пока пуст.")
		return
	}

	keyboard := createFAQCategoriesKeyboard(categories)
	text := "Выберите категорию вопроса:"

	if messageID != 0 {
		// Edit existing message
		params := &gobot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        text,
			ReplyMarkup: keyboard,
		}
		_, err = bot.EditMessageText(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to edit message for FAQ categories", slog.Any("error", err))
		}
	} else {
		// Send new message
		params := &gobot.SendMessageParams{
			ChatID:      chatID,
			Text:        text,
			ReplyMarkup: keyboard,
		}
		_, err = bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send message for FAQ categories", slog.Any("error", err))
		}
	}
}

// showInstructionPlatforms отправляет или редактирует сообщение со списком платформ инструкций
func (b *Bot) showInstructionPlatforms(ctx context.Context, bot *gobot.Bot, chatID int64, messageID int) {
	platforms, err := b.instructionService.GetPlatforms(ctx)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get instruction platforms", slog.Any("error", err))
		b.sendUserError(ctx, bot, chatID, "Не удалось загрузить платформы инструкций.")
		return
	}

	if len(platforms) == 0 {
		b.sendUserMessage(ctx, bot, chatID, "Раздел инструкций пока пуст.")
		return
	}

	keyboard := createInstructionPlatformsKeyboard(platforms)
	text := "Выберите платформу для настройки:"

	if messageID != 0 {
		// Edit existing message
		params := &gobot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        text,
			ReplyMarkup: keyboard,
		}
		_, err = bot.EditMessageText(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to edit message for instruction platforms", slog.Any("error", err))
		}
	} else {
		// Send new message
		params := &gobot.SendMessageParams{
			ChatID:      chatID,
			Text:        text,
			ReplyMarkup: keyboard,
		}
		_, err = bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send message for instruction platforms", slog.Any("error", err))
		}
	}
}

package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"xray-vpn-tg-bot/internal/apperrors"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// --- Структуры для ProviderData Yookassa ---

type ProviderData struct {
	Receipt Receipt `json:"receipt"`
}

type Receipt struct {
	Items         []ReceiptItem `json:"items"`
	TaxSystemCode int           `json:"tax_system_code,omitempty"` // Указывайте, если обязательно для вашей интеграции
	// Customer можно опустить, так как используем NeedPhoneNumber/NeedEmail
}

type ReceiptItem struct {
	Description    string `json:"description"`     // Наименование товара
	Quantity       string `json:"quantity"`        // Количество (строкой)
	Amount         Amount `json:"amount"`          // Сумма (цена * количество)
	VatCode        int    `json:"vat_code"`        // Ставка НДС (1: без НДС, 2: 0%, 3: 10%, 4: 20%, 5: 10/110, 6: 20/120)
	PaymentMode    string `json:"payment_mode"`    // Признак способа расчета (full_payment, full_prepayment, prepayment, advance, partial_payment, credit, credit_payment)
	PaymentSubject string `json:"payment_subject"` // Признак предмета расчета (commodity, service, job, intellectual_activity, payment, etc.)
}

type Amount struct {
	Value    string `json:"value"`    // Сумма в рублях (строкой, например "100.00")
	Currency string `json:"currency"` // Валюта (RUB)
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

	// Initiate payment: create pending record in DB and get its ID as payload
	payload, err := b.paymentService.InitiatePayment(ctx, user.ID, plan.ID)
	if err != nil {
		// Service layer logs details. Check for specific user-facing errors.
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && (appErr.Code == apperrors.ErrCodeNotFound || appErr.Code == apperrors.ErrCodeValidation) {
			b.sendUserError(ctx, bot, chatID, appErr.Message)
		} else {
			b.sendInternalError(ctx, bot, chatID) // Generic error for internal issues
		}
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}

	// Send invoice using Telegram Payments
	prices := []models.LabeledPrice{{
		Label:  plan.Name,
		Amount: int(plan.Price * 100), // Amount in smallest currency unit (kopecks for RUB)
	}}

	// --- Формируем ProviderData для Yookassa ---
	providerData := ProviderData{
		Receipt: Receipt{
			Items: []ReceiptItem{
				{
					Description: plan.Name,
					Quantity:    "1.00", // Количество как строка
					Amount: Amount{
						Value:    fmt.Sprintf("%.2f", plan.Price), // Цена в рублях, форматированная как строка "123.00"
						Currency: strings.ToUpper(plan.Currency),
					},
					VatCode:        1,                 // 1: Без НДС (уточните ваш код НДС)
					PaymentMode:    "full_prepayment", // Или full_payment, зависит от момента оказания услуги (уточните)
					PaymentSubject: "service",         // Предмет расчета: услуга (уточните)
				},
			},
			TaxSystemCode: 1, // 1: УСН Доходы (уточните вашу систему налогообложения)
		},
	}

	providerDataJSON, err := json.Marshal(providerData)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to marshal provider data for invoice", slog.String("plan_id", plan.ID.Hex()), slog.Any("error", err))
		b.sendInternalError(ctx, bot, chatID)
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
		return
	}
	providerDataString := string(providerDataJSON)
	// --- Конец формирования ProviderData ---

	params := &gobot.SendInvoiceParams{
		ChatID:                    chatID,
		Title:                     plan.Name,                                                // Invoice title
		Description:               fmt.Sprintf("Подписка VPN на %s", plan.DurationString()), // Invoice description
		Payload:                   payload,                                                  // Our internal payment ID
		ProviderToken:             b.cfg.Telegram.ProviderToken,                             // Use ProviderToken from config
		Currency:                  strings.ToUpper(plan.Currency),                           // Currency code (e.g., "RUB")
		Prices:                    prices,
		NeedName:                  false, // Имя не требуется
		NeedPhoneNumber:           true,  // Запрашиваем телефон
		NeedEmail:                 false, // Email не требуется
		NeedShippingAddress:       false,
		SendPhoneNumberToProvider: true, // Отправляем телефон провайдеру (Yookassa)
		SendEmailToProvider:       false,
		IsFlexible:                false,              // Set to true if prices depend on shipping
		ProviderData:              providerDataString, // Передаем данные для чека Yookassa
		ReplyMarkup:               nil,                // Explicitly set to nil if no keyboard is needed
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

	// Логируем сгенерированную ссылку для отладки
	b.logger.InfoContext(ctx, "Generated config link",
		slog.String("sub_id", sub.ID.Hex()),
		slog.String("config_link", configLink))

	// --- Получаем QR-код напрямую от 3x-ui --- //
	qrCode, err := b.subscriptionService.GetSubscriptionQRCode(ctx, sub)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get QR code from 3x-ui", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
		// Если не удалось получить QR-код, отправляем только ссылку
		// Убираем клавиатуру, так как она не нужна для простого сообщения

		// Экранируем специальные символы в сообщении
		escapedConfigLink := escapeMarkdownV2(configLink)

		// Проверяем результат экранирования
		b.logger.InfoContext(ctx, "Escaped config link for markdown",
			slog.String("original", configLink),
			slog.String("escaped", escapedConfigLink))

		// Изменяем текст, указывая на возможность копирования
		msg := "Ваша ссылка для подключения (нажмите, чтобы скопировать):\n" +
			"`" + escapedConfigLink + "`" +
			"\n\n(Не удалось получить QR-код от сервера)"

		messageParams := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      msg,
			ParseMode: "MarkdownV2",
			// ReplyMarkup: keyboard, // Убрали клавиатуру
		}

		_, err := bot.SendMessage(ctx, messageParams)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send message with config link",
				slog.String("sub_id", sub.ID.Hex()),
				slog.Any("error", err),
				slog.String("message_text", msg))
			// Fallback to simple text message без форматирования
			b.sendUserMessage(ctx, bot, chatID, fmt.Sprintf("Ваша ссылка для подключения:\n%s", configLink))
		}

		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Ссылка отправлена"})
		return
	}

	// --- Отправляем QR-код от 3x-ui и ссылку --- //
	// Экранируем специальные символы в конфигурационной ссылке
	escapedConfigLink := escapeMarkdownV2(configLink)

	// Проверяем результат экранирования
	b.logger.InfoContext(ctx, "Escaped config link for QR message",
		slog.String("original", configLink),
		slog.String("escaped", escapedConfigLink))

	// Отправляем сначала текстовое сообщение с описанием
	firstMsgText := "Ваша ссылка для подключения:"
	firstMsgParams := &gobot.SendMessageParams{
		ChatID: chatID,
		Text:   firstMsgText,
	}

	_, err = bot.SendMessage(ctx, firstMsgParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send first message",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
	}

	// Отправляем второе сообщение с самой ссылкой
	secondMsgParams := &gobot.SendMessageParams{
		ChatID:    chatID,
		Text:      "`" + escapedConfigLink + "`",
		ParseMode: "MarkdownV2",
	}

	_, err = bot.SendMessage(ctx, secondMsgParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send link message",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err),
			slog.String("escaped_link", escapedConfigLink))
		// Фолбэк - отправляем ссылку без форматирования
		b.sendUserMessage(ctx, bot, chatID, configLink)
	}

	// Отправляем изображение QR-кода отдельным сообщением
	photoParams := &gobot.SendPhotoParams{
		ChatID:  chatID,
		Photo:   &models.InputFileUpload{Filename: "config_qr.png", Data: bytes.NewReader(qrCode)},
		Caption: "QR-код для подключения",
	}

	b.logger.InfoContext(ctx, "Attempting to send QR code photo",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int("photo_size_bytes", len(qrCode)))

	_, err = bot.SendPhoto(ctx, photoParams)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send QR code photo",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
		b.sendUserMessage(ctx, bot, chatID, "Не удалось отправить QR-код")
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
		MessageID:   update.CallbackQuery.Message.Message.ID,
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
	parts := strings.Split(strings.TrimPrefix(callbackData, callbackActionConfigureServer), ":")
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
		b.sendUserMessage(ctx, bot, chatID, fmt.Sprintf("✅ Подписка успешно настроена на сервер '%s'!", serverName)) // Simple fallback
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: "Сервер настроен!"})
		return
	}

	successMessage := fmt.Sprintf("✅ Подписка успешно настроена на сервер '%s'!\n\nНажмите кнопку ниже, чтобы получить конфигурацию.", serverName)
	editParams := &gobot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   update.CallbackQuery.Message.Message.ID,
		Text:        escapeMarkdownV2(successMessage),
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

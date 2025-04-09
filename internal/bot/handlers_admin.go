package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// adminHandler обрабатывает запрос на вход в админ-панель
func (b *Bot) adminHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}

	// Проверяем, является ли пользователь администратором
	isAdmin, err := b.userService.IsAdmin(ctx, user.ID)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to check admin status", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
		b.sendInternalError(ctx, bot, update.Message.Chat.ID)
		return
	}

	if !isAdmin {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "У вас нет доступа к панели администратора.")
		return
	}

	// Если пользователь - администратор, отображаем панель администратора
	params := &gobot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        "🔐 Панель администратора",
		ReplyMarkup: AdminMenuKeyboard(),
	}

	_, err = bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send admin panel message", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
	}
}

// makeAdminHandler обрабатывает команду /makeadmin для назначения пользователя администратором
// Формат: /makeadmin {telegram_id} {true/false}
func (b *Bot) makeAdminHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Не удалось получить информацию о пользователе.")
		return
	}

	// Проверяем, является ли текущий пользователь администратором
	isAdmin, err := b.userService.IsAdmin(ctx, user.ID)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to check admin status", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
		b.sendInternalError(ctx, bot, update.Message.Chat.ID)
		return
	}

	if !isAdmin {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "У вас нет прав для назначения администраторов.")
		return
	}

	// Парсим аргументы команды
	args := strings.Fields(update.Message.Text)
	if len(args) < 2 {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Использование: /makeadmin {telegram_id} [true/false]")
		return
	}

	// Получаем telegram_id
	targetTelegramID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Неверный формат Telegram ID. Используйте числовой ID пользователя.")
		return
	}

	// Определяем статус администратора (по умолчанию - true)
	setAdmin := true
	if len(args) > 2 && strings.ToLower(args[2]) == "false" {
		setAdmin = false
	}

	// Назначаем/снимаем пользователя как администратора
	err = b.userService.SetAdmin(ctx, targetTelegramID, setAdmin)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to set admin status", slog.Int64("target_telegram_id", targetTelegramID), slog.Bool("is_admin", setAdmin), slog.Any("error", err))
		b.sendInternalError(ctx, bot, update.Message.Chat.ID)
		return
	}

	action := "назначен"
	if !setAdmin {
		action = "снят с должности"
	}

	// Отправляем сообщение об успешной операции
	message := "Пользователь с Telegram ID " + args[1] + " успешно " + action + " администратором."
	b.sendUserMessage(ctx, bot, update.Message.Chat.ID, message)
}

// --- Admin Panel Handlers --- //

// adminPanelHandler обрабатывает запрос к админ-панели
func (b *Bot) adminPanelHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		b.logger.WarnContext(ctx, "User not found in context when handling admin panel")
		b.sendUserError(ctx, bot, update.Message.Chat.ID, "Ошибка: пользователь не найден")
		return
	}

	b.logger.InfoContext(ctx, "Handling admin panel request", slog.String("user_id", user.ID.Hex()))

	// Создаем клавиатуру админ-панели
	keyboard := &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: AdminMenuButtonStats}, {Text: AdminMenuButtonServers}},
			{{Text: AdminMenuButtonUsers}, {Text: AdminMenuButtonPlans}},
			{{Text: AdminMenuButtonBroadcast}, {Text: AdminMenuButtonSettings}},
			{{Text: AdminMenuButtonBackToMain}},
		},
	}

	// Отправляем сообщение с приветствием администратора
	params := &gobot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        "👑 *Панель администратора*\n\nВыберите раздел для управления:",
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: keyboard,
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send admin panel message", slog.Int64("chat_id", update.Message.Chat.ID), slog.Any("error", err))
		b.sendInternalError(ctx, bot, update.Message.Chat.ID)
	}
}

// handleAdminServersHandler показывает меню управления серверами
func (b *Bot) handleAdminServersHandler(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	servers, err := b.serverService.ListAllServers(ctx)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get servers for admin panel", slog.Any("error", err))
		b.sendInternalError(ctx, bot, chatID)
		return
	}

	msg := "*Управление серверами*\n\nВыберите сервер для просмотра или добавьте новый."
	_, err = bot.SendMessage(ctx, &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        escapeMarkdownV2(msg),
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: manageServersKeyboard(servers), // Используем новую клавиатуру
	})

	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send admin servers message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// --- Admin Server Callback Handlers --- //

// handleAdminServerAddCallback initiates the process of adding a new server.
func (b *Bot) handleAdminServerAddCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	b.logger.InfoContext(ctx, "Handling 'Admin Add Server' callback", slog.String("admin_user_id", user.ID.Hex()))

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	// Запускаем диалог для добавления сервера
	chatID := cb.From.ID

	// Инициализируем состояние диалога
	b.clearDialogState(chatID) // Очищаем предыдущее состояние
	b.setDialogState(chatID, "state", serverAddStateWaitName)

	// Формируем инструкцию для первого шага (имя сервера)
	instructionText := "➕ *Добавление нового сервера*\n\n" +
		"Шаг 1 из 7: Введите *имя сервера* (например, `Amsterdam-1`).\n\n" +
		"Чтобы отменить процесс на любом этапе, напишите /cancel"

	params := &gobot.SendMessageParams{
		ChatID:    chatID,
		Text:      escapeMarkdownV2(instructionText),
		ParseMode: "MarkdownV2",
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send server add step 1 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// handleAdminServerCancelAddCallback обрабатывает отмену добавления сервера
func (b *Bot) handleAdminServerCancelAddCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	b.logger.InfoContext(ctx, "Handling 'Cancel Add Server' callback", slog.String("admin_user_id", user.ID.Hex()))

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	// Отправляем сообщение о том, что добавление сервера отменено
	chatID := cb.From.ID
	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        "❌ Добавление сервера отменено.",
		ReplyMarkup: serverManagementKeyboard(),
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send server add canceled message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// handleAdminServerAddConfirmCallback обрабатывает подтверждение добавления сервера
func (b *Bot) handleAdminServerAddConfirmCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	chatID := cb.From.ID

	b.logger.InfoContext(ctx, "Handling 'Confirm Add Server' callback", slog.String("admin_user_id", user.ID.Hex()))

	// Получаем данные сервера из состояния диалога
	name := b.getDialogStateString(chatID, "name")
	apiHost := b.getDialogStateString(chatID, "api_host")
	publicHost := b.getDialogStateString(chatID, "public_host")
	username := b.getDialogStateString(chatID, "username")
	password := b.getDialogStateString(chatID, "password")
	location := b.getDialogStateString(chatID, "location")
	inboundId := b.getDialogStateInt(chatID, "inbound_id")

	// Если не задан inboundId, используем значение по умолчанию
	if inboundId == 0 {
		inboundId = serverAddDefaultInbound
	}

	// Проверяем, что все обязательные поля заполнены
	if name == "" || apiHost == "" || publicHost == "" || username == "" || password == "" {
		b.logger.WarnContext(ctx, "Missing required server data",
			slog.String("admin_user_id", user.ID.Hex()),
			slog.String("name", name),
			slog.String("api_host", apiHost),
			slog.String("public_host", publicHost))

		// Отвечаем на callback
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка: не все обязательные поля заполнены",
			ShowAlert:       true,
		})

		// Очищаем состояние диалога
		b.clearDialogState(chatID)
		return
	}

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	// Добавляем сервер через сервис
	b.logger.InfoContext(ctx, "Adding server",
		slog.String("admin_user_id", user.ID.Hex()),
		slog.String("name", name),
		slog.String("api_host", apiHost))

	server, err := b.serverService.AddServer(ctx, name, publicHost, apiHost, username, password, location, inboundId)

	// После завершения операции очищаем состояние диалога независимо от результата
	b.clearDialogState(chatID)

	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to add server",
			slog.String("admin_user_id", user.ID.Hex()),
			slog.String("server_name", name),
			slog.Any("error", err))

		// Отправляем сообщение об ошибке
		params := &gobot.SendMessageParams{
			ChatID: chatID,
			Text:   "❌ Ошибка при добавлении сервера: " + err.Error(),
		}
		_, _ = bot.SendMessage(ctx, params)
		return
	}

	// Отправляем сообщение об успешном добавлении сервера
	successMsg := fmt.Sprintf("✅ Сервер успешно добавлен!\n\n"+
		"📋 *Информация о сервере:*\n"+
		"ID: `%s`\n"+
		"Имя: `%s`\n"+
		"Расположение: `%s`\n"+
		"Публичный хост: `%s`\n"+
		"API хост: `%s`\n"+
		"ID Inbound: `%d`",
		server.ID.Hex(), server.Name, server.Location,
		server.PublicHost, server.ApiHost, server.TargetInboundID)

	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        escapeMarkdownV2(successMsg),
		ParseMode:   "MarkdownV2",
		ReplyMarkup: serverManagementKeyboard(),
	}

	_, sendErr := bot.SendMessage(ctx, params)
	if sendErr != nil {
		b.logger.ErrorContext(ctx, "Failed to send server added success message",
			slog.Int64("chat_id", chatID),
			slog.Any("error", sendErr))
	}
}

// handleAdminServerTextInput обрабатывает текстовые сообщения при вводе данных сервера
func (b *Bot) handleAdminServerTextInput(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	user := UserFromContext(ctx)
	if user == nil {
		return
	}

	// Получаем ID чата и текст сообщения
	chatID := update.Message.Chat.ID
	messageText := update.Message.Text

	// Если пользователь отправил /cancel, отменяем процесс
	if messageText == "/cancel" {
		b.clearDialogState(chatID)
		b.sendUserMessage(ctx, bot, chatID, "❌ Добавление сервера отменено.")
		return
	}

	// Получаем текущее состояние диалога
	state := b.getDialogStateString(chatID, "state")
	if state == "" {
		// Нет активного диалога, ничего не делаем
		return
	}

	// Обрабатываем текущий шаг диалога
	switch state {
	case serverAddStateWaitName:
		// Сохраняем имя сервера
		b.setDialogState(chatID, "name", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitApiHost)

		// Запрашиваем API хост
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 2 из 7: Введите *API хост* сервера (например, `http://38.244.152.237:54321`).\n\n" +
			"Это URL-адрес панели X-UI."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 2 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitApiHost:
		// Сохраняем API хост
		b.setDialogState(chatID, "api_host", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitPublicHost)

		// Запрашиваем публичный хост
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 3 из 7: Введите *публичный хост* сервера (например, `38.244.152.237` или `myserver.domain.com`).\n\n" +
			"Это адрес, который будет использоваться в конфигурации для подключения клиентов."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 3 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitPublicHost:
		// Сохраняем публичный хост
		b.setDialogState(chatID, "public_host", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitUsername)

		// Запрашиваем логин
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 4 из 7: Введите *логин* для доступа к API сервера."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 4 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitUsername:
		// Сохраняем логин
		b.setDialogState(chatID, "username", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitPassword)

		// Запрашиваем пароль
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 5 из 7: Введите *пароль* для доступа к API сервера."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 5 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitPassword:
		// Сохраняем пароль
		b.setDialogState(chatID, "password", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitLocation)

		// Запрашиваем локацию
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 6 из 7: Введите *расположение* сервера (например, `Amsterdam, NL` или `Эстония`).\n\n" +
			"Это информация будет отображаться пользователям при выборе сервера."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 6 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitLocation:
		// Сохраняем локацию
		b.setDialogState(chatID, "location", messageText)
		b.setDialogState(chatID, "state", serverAddStateWaitInbound)

		// Запрашиваем inbound ID
		instructionText := "➕ *Добавление нового сервера*\n\n" +
			"Шаг 7 из 7: Введите *ID входящего соединения* (Inbound ID) в панели X-UI.\n\n" +
			"Обычно это число от 1 до 10. По умолчанию используется 1."

		params := &gobot.SendMessageParams{
			ChatID:    chatID,
			Text:      escapeMarkdownV2(instructionText),
			ParseMode: "MarkdownV2",
		}
		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server add step 7 message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		}

	case serverAddStateWaitInbound:
		// Пытаемся преобразовать введенный текст в число
		inboundID := serverAddDefaultInbound // Значение по умолчанию
		if messageText != "" {
			if val, err := strconv.Atoi(messageText); err == nil {
				inboundID = val
			}
		}

		// Сохраняем inbound ID
		b.setDialogState(chatID, "inbound_id", inboundID)
		b.setDialogState(chatID, "state", serverAddStateConfirmation)

		// Получаем все сохраненные данные
		name := b.getDialogStateString(chatID, "name")
		apiHost := b.getDialogStateString(chatID, "api_host")
		publicHost := b.getDialogStateString(chatID, "public_host")
		username := b.getDialogStateString(chatID, "username")
		password := b.getDialogStateString(chatID, "password")
		location := b.getDialogStateString(chatID, "location")

		// Формируем сообщение для подтверждения
		confirmMsg := fmt.Sprintf("📋 *Данные нового сервера:*\n\n"+
			"Имя: `%s`\n"+
			"Расположение: `%s`\n"+
			"Публичный хост: `%s`\n"+
			"API хост: `%s`\n"+
			"Учетная запись: `%s` / `%s`\n"+
			"ID Inbound: `%d`\n\n"+
			"Проверьте данные и подтвердите добавление сервера или отмените операцию.",
			name, location, publicHost, apiHost, username, password, inboundID)

		// Отправляем сообщение с данными сервера и кнопками подтверждения
		params := &gobot.SendMessageParams{
			ChatID:      chatID,
			Text:        escapeMarkdownV2(confirmMsg),
			ParseMode:   "MarkdownV2",
			ReplyMarkup: createServerAddConfirmationKeyboard(),
		}

		_, err := bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send server confirmation message",
				slog.Int64("chat_id", chatID),
				slog.Any("error", err))
		}

	default:
		// Неизвестное состояние, сбрасываем диалог
		b.clearDialogState(chatID)
		b.sendUserMessage(ctx, bot, chatID, "❌ Произошла ошибка в диалоге. Добавление сервера отменено.")
	}
}

// handleAdminServerListCallback displays the list of configured servers.
func (b *Bot) handleAdminServerListCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	b.logger.InfoContext(ctx, "Handling 'Admin List Servers' callback", slog.String("admin_user_id", user.ID.Hex()))

	servers, err := b.serverService.ListAllServers(ctx)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to list all servers for admin", slog.String("admin_user_id", user.ID.Hex()), slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка при получении списка серверов.", ShowAlert: true})
		return
	}

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	var msgText strings.Builder
	msgText.WriteString("📜 *Список всех серверов:*\n\n")

	if len(servers) == 0 {
		msgText.WriteString("_Нет настроенных серверов._")
	} else {
		for i, srv := range servers {
			enabledEmoji := "❌"
			if srv.IsEnabled {
				enabledEmoji = "✅"
			}
			msgText.WriteString(fmt.Sprintf("%d. *%s* (%s) %s `[%s]`\n",
				i+1, srv.Name, srv.Location, enabledEmoji, srv.ID.Hex()))
		}
	}

	// Отправляем новое сообщение в личку админу
	chatID := cb.From.ID
	sendParams := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        msgText.String(),
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: manageServersKeyboard(servers), // Добавляем клавиатуру со списком серверов
	}
	_, sendErr := bot.SendMessage(ctx, sendParams)
	if sendErr != nil {
		b.logger.ErrorContext(ctx, "Failed to send server list message", slog.Int64("chat_id", chatID), slog.Any("error", sendErr))
	}
}

// handleAdminServerBackCallback handles the back button from server management view.
func (b *Bot) handleAdminServerBackCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	b.logger.InfoContext(ctx, "Handling 'Admin Back from Servers' callback", slog.String("admin_user_id", user.ID.Hex()))

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	// Отправляем новое сообщение с меню управления серверами в личку админу
	chatID := cb.From.ID
	params := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        "⚙️ *Управление серверами*\n\nВыберите действие:",
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: serverManagementKeyboard(),
	}

	_, err := bot.SendMessage(ctx, params)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to send server management menu message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

// handleAdminDialogInput routes admin text input to the appropriate dialog handler.
func (b *Bot) handleAdminDialogInput(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	state := b.getDialogStateString(chatID, "state")

	// Currently, only server adding dialog exists
	if strings.HasPrefix(state, serverAddStatePrefix) {
		b.handleAdminServerTextInput(ctx, bot, update)
	} else {
		// Handle other potential admin dialogs here in the future
		b.logger.WarnContext(ctx, "Received admin input for unknown dialog state",
			slog.Int64("chat_id", chatID),
			slog.String("state", state),
			slog.String("text", update.Message.Text))
		// Optionally clear the state or send an error message
		b.clearDialogState(chatID)
		b.sendUserMessage(ctx, bot, chatID, "Неизвестное состояние диалога. Операция отменена.")
	}
}

// handleAdminServerViewCallback - Обработчик для просмотра деталей сервера
func (b *Bot) handleAdminServerViewCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	// Извлекаем serverID из callbackData
	callbackData := cb.Data
	serverIDHex := strings.TrimPrefix(callbackData, callbackAdminServerView)

	b.logger.InfoContext(ctx, "Handling admin server view callback",
		slog.String("admin_user_id", user.ID.Hex()),
		slog.String("server_id", serverIDHex))

	// Получаем информацию о сервере
	server, err := b.serverService.GetServer(ctx, serverIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get server details",
			slog.String("server_id", serverIDHex),
			slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка получения данных сервера",
			ShowAlert:       true})
		return
	}

	// Готовим сообщение с информацией о сервере
	statusText := "Отключен"
	healthText := "Недоступен"

	if server.IsEnabled {
		statusText = "Активен"
	}

	if server.IsHealthy {
		healthText = "Доступен"
	}

	msgText := fmt.Sprintf("*Информация о сервере*\n\n"+
		"ID: `%s`\n"+
		"Имя: `%s`\n"+
		"Локация: `%s`\n"+
		"Публичный хост: `%s`\n"+
		"API хост: `%s`\n"+
		"Inbound ID: `%d`\n"+
		"Статус: `%s`\n"+
		"Соединение: `%s`",
		server.ID.Hex(), server.Name, server.Location,
		server.PublicHost, server.ApiHost, server.TargetInboundID,
		statusText, healthText)

	// Создаем клавиатуру для управления сервером
	keyboard := viewServerKeyboard(server)

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID})

	// Отправляем новое сообщение с информацией о сервере
	chatID := cb.From.ID
	sendParams := &gobot.SendMessageParams{
		ChatID:      chatID,
		Text:        escapeMarkdownV2(msgText),
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	}

	_, sendErr := bot.SendMessage(ctx, sendParams)
	if sendErr != nil {
		b.logger.ErrorContext(ctx, "Failed to send server details message",
			slog.Int64("chat_id", chatID),
			slog.Any("error", sendErr))
	}
}

// handleAdminServerDeleteConfirmCallback - Обработчик для подтверждения удаления сервера
func (b *Bot) handleAdminServerDeleteConfirmCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	// Извлекаем serverID из callbackData
	callbackData := cb.Data
	serverIDHex := strings.TrimPrefix(callbackData, callbackAdminServerDeleteConfirm)

	b.logger.InfoContext(ctx, "Handling admin server delete confirm callback",
		slog.String("admin_user_id", user.ID.Hex()),
		slog.String("server_id", serverIDHex))

	// Проверяем, что сервер существует перед удалением
	server, err := b.serverService.GetServer(ctx, serverIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get server details for delete",
			slog.String("server_id", serverIDHex),
			slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка: сервер не найден",
			ShowAlert:       true})
		return
	}

	// Запоминаем имя сервера для сообщения об успешном удалении
	serverName := server.Name

	// Удаляем сервер
	err = b.serverService.DeleteServer(ctx, serverIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to delete server",
			slog.String("server_id", serverIDHex),
			slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка при удалении сервера",
			ShowAlert:       true})
		return
	}

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            "Сервер удален",
	})

	// Отправляем сообщение о успешном удалении
	chatID := cb.From.ID
	msgText := fmt.Sprintf("✅ Сервер *%s* успешно удален", serverName)

	sendParams := &gobot.SendMessageParams{
		ChatID:    chatID,
		Text:      escapeMarkdownV2(msgText),
		ParseMode: "MarkdownV2",
	}

	_, _ = bot.SendMessage(ctx, sendParams)

	// Показываем список серверов
	b.handleAdminServerListCallback(ctx, bot, update)
}

// handleAdminServerDeleteCancelCallback - Обработчик для отмены удаления сервера
func (b *Bot) handleAdminServerDeleteCancelCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	// Извлекаем serverID из callbackData
	callbackData := cb.Data
	serverIDHex := strings.TrimPrefix(callbackData, callbackAdminServerDeleteCancel)

	b.logger.InfoContext(ctx, "Handling admin server delete cancel callback",
		slog.String("admin_user_id", user.ID.Hex()),
		slog.String("server_id", serverIDHex))

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            "Удаление отменено",
	})

	// Возвращаемся к просмотру сервера
	viewUpdate := &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   cb.ID,
			From: cb.From,
			Data: callbackAdminServerView + serverIDHex,
		},
	}

	b.handleAdminServerViewCallback(ctx, bot, viewUpdate)
}

// handleAdminServerToggleCallback - Обработчик для включения/выключения сервера
func (b *Bot) handleAdminServerToggleCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	user := UserFromContext(ctx)
	if user == nil {
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: "Ошибка: пользователь не найден", ShowAlert: true})
		return
	}

	// Извлекаем serverID из callbackData
	callbackData := cb.Data
	serverIDHex := strings.TrimPrefix(callbackData, callbackAdminServerToggle)

	b.logger.InfoContext(ctx, "Handling admin server toggle callback",
		slog.String("admin_user_id", user.ID.Hex()),
		slog.String("server_id", serverIDHex))

	// Получаем информацию о сервере
	server, err := b.serverService.GetServer(ctx, serverIDHex)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to get server details for toggle",
			slog.String("server_id", serverIDHex),
			slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка получения данных сервера",
			ShowAlert:       true})
		return
	}

	// Меняем статус на противоположный
	server.IsEnabled = !server.IsEnabled

	// Обновляем сервер в базе данных
	err = b.serverService.UpdateServer(ctx, serverIDHex, server)
	if err != nil {
		b.logger.ErrorContext(ctx, "Failed to update server status",
			slog.String("server_id", serverIDHex),
			slog.Bool("new_status", server.IsEnabled),
			slog.Any("error", err))
		_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Ошибка при обновлении статуса сервера",
			ShowAlert:       true})
		return
	}

	// Формируем текст ответа
	statusText := "отключен"
	if server.IsEnabled {
		statusText = "включен"
	}

	// Отвечаем на callback
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            "Сервер " + statusText,
	})

	// Отправляем обновленную информацию о сервере, используя тот же метод, что и при просмотре
	// Создаем фиктивный update с тем же callback для вызова handleAdminServerViewCallback
	viewUpdate := &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   cb.ID,
			From: cb.From,
			Data: callbackAdminServerView + serverIDHex,
		},
	}

	b.handleAdminServerViewCallback(ctx, bot, viewUpdate)
}

// handleAdminServerBackToListCallback - Обработчик для возврата к списку серверов
func (b *Bot) handleAdminServerBackToListCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	// TODO: Показать список серверов (вызвать handleAdminServerListCallback или похожую логику)
	// Возможно, нужно будет отредактировать исходное сообщение
	// Вместо простого ответа, здесь нужно отредактировать сообщение со списком серверов
	b.handleAdminServerListCallback(ctx, bot, update) // Вызываем обработчик списка
}

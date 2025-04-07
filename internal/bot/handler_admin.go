package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"xray-vpn-tg-bot/internal/domain"

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

// Здесь будут добавлены другие обработчики для админ-панели, например:
// - statsHandler - показывает статистику
// - manageServersHandler - управление серверами
// - manageUsersHandler - управление пользователями
// - backToMainMenuHandler - возвращение в главное меню

// getUserKeyboard возвращает клавиатуру с учетом статуса администратора
func (b *Bot) getUserKeyboard(ctx context.Context, user *domain.User) models.ReplyKeyboardMarkup {
	// Если пользователь не определен, возвращаем стандартную клавиатуру
	if user == nil {
		return *mainMenuKeyboard()
	}

	// Проверяем статус администратора
	isAdmin := false

	// Проверяем, является ли пользователь администратором по telegram_id из конфигурации
	if b.cfg.Telegram.AdminID != 0 && user.TelegramID == b.cfg.Telegram.AdminID {
		isAdmin = true
	} else {
		// Проверяем флаг is_admin в объекте пользователя
		adminStatus, err := b.userService.IsAdmin(ctx, user.ID)
		if err == nil && adminStatus {
			isAdmin = true
		}
	}

	// Логируем информацию для отладки
	b.logger.DebugContext(ctx, "Generating user keyboard",
		slog.String("user_id", user.ID.Hex()),
		slog.Int64("telegram_id", user.TelegramID),
		slog.Bool("is_admin", isAdmin))

	// Возвращаем соответствующую клавиатуру
	if isAdmin {
		return *MainMenuKeyboardWithAdmin(true)
	} else {
		return *mainMenuKeyboard()
	}
}

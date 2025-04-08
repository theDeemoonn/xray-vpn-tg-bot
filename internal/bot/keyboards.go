package bot

import (
	"context"
	"fmt"
	"log/slog"
	"xray-vpn-tg-bot/internal/domain"

	"github.com/go-telegram/bot/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// --- Константы для кнопок главного меню ---
const (
	MainMenuButtonMySubscriptions = "🔑 Мои подписки"
	MainMenuButtonBuySubscription = "🛒 Купить подписку"
	MainMenuButtonInstructions    = "📱 Инструкции"
	MainMenuButtonFAQ             = "❓ FAQ"
	MainMenuButtonReferral        = "🤝 Реферальная программа"
	MainMenuButtonSupport         = "💬 Поддержка"
	MainMenuButtonAdmin           = "👑 Админ-панель"
)

// --- Константы для кнопок админ-панели ---
const (
	AdminMenuButtonStats      = "📊 Статистика"
	AdminMenuButtonServers    = "⚙️ Управление серверами"
	AdminMenuButtonUsers      = "👤 Управление пользователями"
	AdminMenuButtonPlans      = "📋 Управление тарифами"
	AdminMenuButtonBroadcast  = "📢 Рассылка"
	AdminMenuButtonSettings   = "🔧 Настройки"
	AdminMenuButtonBackToMain = "⬅️ Главное меню"
)

// --- Константы для кнопок управления серверами ---
const (
	AdminServersButtonAdd  = "➕ Добавить сервер"
	AdminServersButtonList = "📜 Список серверов"
	AdminServersButtonBack = "⬅️ Назад в админ-панель"
)

// --- Префиксы и константы callback data ---
const (
	// Основные действия
	callbackActionSelectPlan      = "plan:"
	callbackActionSelectServer    = "srv_sel:"
	callbackActionConfigureServer = "srv_conf:" // subscriptionID:serverID
	callbackActionGetConfig       = "get_cfg:"  // subscriptionID
	callbackActionAdmin           = "admin:"
	callbackActionSupport         = "support"

	// FAQ & Инструкции
	callbackPrefixFAQCategory         = "faq_cat:"
	callbackPrefixFAQQuestion         = "faq_q:"
	callbackPrefixInstructionPlatform = "instr_plat:"
	callbackPrefixInstructionDetails  = "instr_det:"
	callbackPrefixBackToFAQ           = "back_faq"
	callbackPrefixBackToInstructions  = "back_instr"
	callbackPrefixBackToHelpRoot      = "back_help"

	// Обработчики вызываемые в bot.go
	callbackActionFAQCategory         = callbackPrefixFAQCategory
	callbackActionFAQQuestion         = callbackPrefixFAQQuestion
	callbackActionInstructionPlatform = callbackPrefixInstructionPlatform
	callbackActionInstructionDetails  = callbackPrefixInstructionDetails
	callbackActionBackToFAQ           = callbackPrefixBackToFAQ
	callbackActionBackToInstructions  = callbackPrefixBackToInstructions
	callbackActionBackToHelpRoot      = callbackPrefixBackToHelpRoot

	// Тексты кнопок в HelpRootKeyboard
	buttonTextInstructions = "📱 Инструкции по установке"
	buttonTextFAQ          = "❓ Часто задаваемые вопросы"
	buttonTextSupport      = "💬 Связаться с поддержкой"

	// Админ: Серверы
	callbackPrefixAdminServers       = "adm_srv:"
	callbackAdminServersActionAdd    = callbackPrefixAdminServers + "add"
	callbackAdminServersActionList   = callbackPrefixAdminServers + "list"
	callbackAdminServersActionView   = callbackPrefixAdminServers + "view:"   // view:<server_id>
	callbackAdminServersActionDelete = callbackPrefixAdminServers + "del:"    // del:<server_id>
	callbackAdminServersActionToggle = callbackPrefixAdminServers + "toggle:" // toggle:<server_id>
	callbackAdminServersBackToAdmin  = callbackPrefixAdminServers + "back"

	// Константы для диалога добавления сервера
	callbackServerAddStepCancel  = callbackPrefixAdminServers + "add_cancel"
	callbackServerAddStepConfirm = callbackPrefixAdminServers + "add_confirm"

	// Клавиатуры для добавления сервера
	serverAddCancelButton  = "❌ Отменить"
	serverAddConfirmButton = "✅ Подтвердить"
)

// mainMenuKeyboard возвращает основную клавиатуру меню
func mainMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: MainMenuButtonMySubscriptions}, {Text: MainMenuButtonBuySubscription}},
			{{Text: MainMenuButtonInstructions}, {Text: MainMenuButtonFAQ}},
			{{Text: MainMenuButtonReferral}, {Text: MainMenuButtonSupport}},
			// Кнопка админа добавляется только через MainMenuKeyboardWithAdmin
		},
	}
}

// MainMenuKeyboardWithAdmin возвращает основную клавиатуру меню с дополнительными кнопками администратора
func MainMenuKeyboardWithAdmin(isAdmin bool) *models.ReplyKeyboardMarkup {
	kb := mainMenuKeyboard()

	if isAdmin {
		adminButton := []models.KeyboardButton{{Text: MainMenuButtonAdmin}}
		kb.Keyboard = append(kb.Keyboard, adminButton)
	}

	return kb
}

// createPlansKeyboard создает inline клавиатуру для выбора тарифных планов
func createPlansKeyboard(plans []*domain.Plan) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton

	for _, plan := range plans {
		callbackData := fmt.Sprintf("%s%s", callbackActionSelectPlan, plan.ID.Hex())
		planButton := []models.InlineKeyboardButton{
			{
				Text:         fmt.Sprintf("%s - %.2f %s", plan.Name, plan.Price, plan.Currency),
				CallbackData: callbackData,
			},
		}
		buttons = append(buttons, planButton)
	}

	return models.InlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createSubscriptionActionsKeyboard создает inline клавиатуру для действий с подпиской
func createSubscriptionActionsKeyboard(subscription *domain.Subscription) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton

	// Если подписка не настроена (ServerID пуст), предлагаем настроить
	if subscription.ServerID.IsZero() {
		configureButton := []models.InlineKeyboardButton{
			{
				Text:         "⚙️ Настроить сервер",
				CallbackData: fmt.Sprintf("%s%s", callbackActionSelectServer, subscription.ID.Hex()),
			},
		}
		buttons = append(buttons, configureButton)
	} else {
		// Если настроена, даем возможность получить конфигурацию
		getConfigButton := []models.InlineKeyboardButton{
			{
				Text:         "📥 Получить конфигурацию",
				CallbackData: fmt.Sprintf("%s%s", callbackActionGetConfig, subscription.ID.Hex()),
			},
		}
		buttons = append(buttons, getConfigButton)
	}

	return models.InlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createServerSelectionKeyboard создает inline клавиатуру для выбора сервера
func createServerSelectionKeyboard(servers []*domain.Server, subscriptionID primitive.ObjectID) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton

	for _, server := range servers {
		buttonText := fmt.Sprintf("%s (%s)", server.Name, server.Location)
		callbackData := fmt.Sprintf("%s%s:%s", callbackActionConfigureServer, subscriptionID.Hex(), server.ID.Hex())

		serverButton := []models.InlineKeyboardButton{
			{
				Text:         buttonText,
				CallbackData: callbackData,
			},
		}
		buttons = append(buttons, serverButton)
	}

	return models.InlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// --- Клавиатуры для раздела "FAQ & Поддержка" --- //

// createHelpRootKeyboard создает клавиатуру для главного экрана помощи
func createHelpRootKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         buttonTextInstructions,
					CallbackData: callbackPrefixBackToInstructions, // Reuse for direct entry
				},
			},
			{
				{
					Text:         buttonTextFAQ,
					CallbackData: callbackPrefixBackToFAQ, // Reuse for direct entry
				},
			},
			{
				{
					Text:         buttonTextSupport,
					CallbackData: callbackActionSupport,
				},
			},
		},
	}
}

// createFAQCategoriesKeyboard создает клавиатуру для выбора категории FAQ
func createFAQCategoriesKeyboard(categories []string) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton
	for _, category := range categories {
		row := []models.InlineKeyboardButton{
			{Text: category, CallbackData: fmt.Sprintf("%s%s", callbackPrefixFAQCategory, category)},
		}
		buttons = append(buttons, row)
	}
	// Добавляем кнопку "Назад"
	buttons = append(buttons, createBackButtonRow(callbackPrefixBackToHelpRoot))
	return models.InlineKeyboardMarkup{InlineKeyboard: buttons}
}

// createFAQQuestionsKeyboard создает клавиатуру для вопросов в категории FAQ
func createFAQQuestionsKeyboard(faqs []*domain.FAQ, category string) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton
	for _, faq := range faqs {
		row := []models.InlineKeyboardButton{
			{Text: faq.Question, CallbackData: fmt.Sprintf("%s%s", callbackPrefixFAQQuestion, faq.ID.Hex())},
		}
		buttons = append(buttons, row)
	}
	// Добавляем кнопку "Назад" (к списку категорий)
	buttons = append(buttons, createBackButtonRow(callbackPrefixBackToFAQ))
	return models.InlineKeyboardMarkup{InlineKeyboard: buttons}
}

// createInstructionPlatformsKeyboard создает клавиатуру для выбора платформы инструкций
func createInstructionPlatformsKeyboard(platforms []string) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton
	for _, platform := range platforms {
		row := []models.InlineKeyboardButton{
			{Text: platform, CallbackData: fmt.Sprintf("%s%s", callbackPrefixInstructionPlatform, platform)},
		}
		buttons = append(buttons, row)
	}
	// Добавляем кнопку "Назад"
	buttons = append(buttons, createBackButtonRow(callbackPrefixBackToHelpRoot))
	return models.InlineKeyboardMarkup{InlineKeyboard: buttons}
}

// createInstructionsListKeyboard создает клавиатуру для инструкций на платформе
func createInstructionsListKeyboard(instructions []*domain.Instruction, platform string) models.InlineKeyboardMarkup {
	var buttons [][]models.InlineKeyboardButton
	for _, instruction := range instructions {
		row := []models.InlineKeyboardButton{
			{Text: instruction.Title, CallbackData: fmt.Sprintf("%s%s", callbackPrefixInstructionDetails, instruction.ID.Hex())},
		}
		buttons = append(buttons, row)
	}
	// Добавляем кнопку "Назад" (к списку платформ)
	buttons = append(buttons, createBackButtonRow(callbackPrefixBackToInstructions))
	return models.InlineKeyboardMarkup{InlineKeyboard: buttons}
}

// createBackToFAQKeyboard создает клавиатуру с кнопкой "Назад к FAQ"
func createBackToFAQKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			createBackButtonRow(callbackPrefixBackToFAQ),
		},
	}
}

// createBackToInstructionsKeyboard создает клавиатуру с кнопкой "Назад к инструкциям"
func createBackToInstructionsKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			createBackButtonRow(callbackPrefixBackToInstructions),
		},
	}
}

// --- Вспомогательные функции для клавиатур ---

// createBackButtonRow создает ряд с кнопкой "Назад"
func createBackButtonRow(callbackData string) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		{Text: "⬅️ Назад", CallbackData: callbackData},
	}
}

// --- Functions returning keyboards (if needed, e.g., for admin panel) ---

// AdminMenuKeyboard возвращает клавиатуру панели администратора
func AdminMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: AdminMenuButtonStats}, {Text: AdminMenuButtonServers}},
			{{Text: AdminMenuButtonUsers}, {Text: AdminMenuButtonPlans}},
			{{Text: AdminMenuButtonBackToMain}},
		},
	}
}

// serverManagementKeyboard создает inline клавиатуру для управления серверами
func serverManagementKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: AdminServersButtonAdd, CallbackData: callbackAdminServersActionAdd},
			},
			{
				{Text: AdminServersButtonList, CallbackData: callbackAdminServersActionList},
			},
			{
				{Text: AdminServersButtonBack, CallbackData: callbackAdminServersBackToAdmin},
			},
		},
	}
}

// Функция, которая создает клавиатуру с кнопкой для копирования ссылки
func createConfigLinkKeyboard(configLink string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text: "🔗 Копировать ссылку конфигурации",
					URL:  configLink, // Прямая ссылка для открытия/копирования
				},
			},
		},
	}
}

func createServerAddConfirmationKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: serverAddConfirmButton, CallbackData: callbackServerAddStepConfirm},
				{Text: serverAddCancelButton, CallbackData: callbackServerAddStepCancel},
			},
		},
	}
}

// TODO: Добавить клавиатуры для списка серверов, просмотра/редактирования сервера, подтверждения удаления и т.д.

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

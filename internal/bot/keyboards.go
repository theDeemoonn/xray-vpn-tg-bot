package bot

import (
	"fmt"
	"xray-vpn-tg-bot/internal/domain"

	"github.com/go-telegram/bot/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// --- Main Menu ---
const (
	MainMenuButtonMySubscriptions = "🚀 Мои подписки"
	MainMenuButtonBuySubscription = "🛒 Купить подписку"
	MainMenuButtonReferral        = "🎁 Реф. программа"
	MainMenuButtonInstructions    = "📱 Инструкции"
	MainMenuButtonFAQ             = "❓ FAQ"
	MainMenuButtonSupport         = "💬 Поддержка"
)

// --- Дополнительные константы для клавиатур ---
const (
	callbackActionSupport = "support"

	// Кнопки для главного меню FAQ/Инструкций
	buttonTextFAQ          = "❓ Частые вопросы (FAQ)"
	buttonTextInstructions = "📱 Инструкции по настройке"
	buttonTextSupport      = "💬 Связаться с поддержкой"

	// Префиксы callback data
	callbackPrefixFAQCategory         = "faq_cat:"
	callbackPrefixFAQQuestion         = "faq_q:"
	callbackPrefixInstructionPlatform = "instr_plat:"
	callbackPrefixInstructionDetails  = "instr_det:"
	callbackPrefixBackToFAQ           = "back_faq"
	callbackPrefixBackToInstructions  = "back_instr"
	callbackPrefixBackToHelpRoot      = "back_help"
)

// mainMenuKeyboard возвращает основную клавиатуру меню
func mainMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: MainMenuButtonMySubscriptions}, {Text: MainMenuButtonBuySubscription}},
			{{Text: MainMenuButtonInstructions}, {Text: MainMenuButtonFAQ}},
			{{Text: MainMenuButtonReferral}, {Text: MainMenuButtonSupport}},
			// Add admin buttons here conditionally if needed
		},
	}
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

// Example: adminMenuKeyboard (conditional display logic would be elsewhere)
func adminMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: "📊 Статистика"}, {Text: "⚙️ Управление серверами"}},
			{{Text: "👤 Управление пользователями"}},
			{{Text: "⬅️ Главное меню"}}, // Button to return to the main user menu
		},
	}
}

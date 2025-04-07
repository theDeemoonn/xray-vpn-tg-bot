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
	MainMenuButtonReferral        = "🎁 Реферальная программа"
	MainMenuButtonFAQ             = "❓ FAQ & Поддержка"
)

// --- Дополнительные константы для клавиатур ---
const (
	callbackActionFAQ = "faq_"
)

// mainMenuKeyboard возвращает основную клавиатуру меню
func mainMenuKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: "🚀 Мои подписки"}, {Text: "🛒 Купить подписку"}},
			{{Text: "🎁 Реферальная программа"}, {Text: "❓ FAQ & Поддержка"}},
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

// createFAQKeyboard создает inline клавиатуру для разделов FAQ
func createFAQKeyboard() models.InlineKeyboardMarkup {
	return models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         "📱 Как установить?",
					CallbackData: "faq_install",
				},
			},
			{
				{
					Text:         "🔧 Решение проблем",
					CallbackData: "faq_troubleshoot",
				},
			},
			{
				{
					Text:         "💬 Связаться с поддержкой",
					CallbackData: "faq_support",
				},
			},
		},
	}
}

/* Example Inline Keyboard
func (b *Bot) exampleInlineKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboardMarkup(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("Button 1").WithCallbackData("callback_data_1"),
			tu.InlineKeyboardButton("Button 2").WithCallbackData("callback_data_2"),
		),
	)
}
*/

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

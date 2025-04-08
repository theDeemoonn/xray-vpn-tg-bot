package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"xray-vpn-tg-bot/internal/domain"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// --- FAQ & Instruction Callback Handlers --- //

// handleBackToFAQCallback handles the "Back to FAQ" button press (shows categories)
func (b *Bot) handleBackToFAQCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	b.logger.DebugContext(ctx, "Handling back to FAQ callback", slog.Int64("chat_id", chatID))
	b.showFAQCategories(ctx, bot, chatID, messageID)
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleBackToInstructionsCallback handles the "Back to Instructions" button press (shows platforms)
func (b *Bot) handleBackToInstructionsCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
	b.logger.DebugContext(ctx, "Handling back to Instructions callback", slog.Int64("chat_id", chatID))
	b.showInstructionPlatforms(ctx, bot, chatID, messageID)
	_, _ = bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID})
}

// handleBackToHelpRootCallback handles the "Back" button press from category/platform lists
func (b *Bot) handleBackToHelpRootCallback(ctx context.Context, bot *gobot.Bot, update *models.Update) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID
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
	messageID := update.CallbackQuery.Message.Message.ID
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
	messageID := update.CallbackQuery.Message.Message.ID
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
	messageID := update.CallbackQuery.Message.Message.ID
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
	messageID := update.CallbackQuery.Message.Message.ID
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
	msgText := fmt.Sprintf("*📱 %s*\n*Платформа:* %s\n\n%s", escapeMarkdownV2(instruction.Title), escapeMarkdownV2(instruction.Platform), escapeMarkdownV2(instruction.Content))

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
		b.logger.DebugContext(ctx, "Attempting to edit message for FAQ categories", slog.Int64("chat_id", chatID), slog.Int("message_id", messageID))
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
		b.logger.DebugContext(ctx, "Attempting to send new message for FAQ categories", slog.Int64("chat_id", chatID))
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
		b.logger.DebugContext(ctx, "Attempting to edit message for instruction platforms", slog.Int64("chat_id", chatID), slog.Int("message_id", messageID))
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
		b.logger.DebugContext(ctx, "Attempting to send new message for instruction platforms", slog.Int64("chat_id", chatID))
		_, err = bot.SendMessage(ctx, params)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to send message for instruction platforms", slog.Any("error", err))
		}
	}
}

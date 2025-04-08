package bot

import (
	"context"
	"fmt"
	"log/slog"

	"xray-vpn-tg-bot/internal/config"
	"xray-vpn-tg-bot/internal/service"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Bot represents the Telegram bot application using go-telegram/bot
type Bot struct {
	api                 *gobot.Bot
	cfg                 *config.Config
	logger              *slog.Logger
	userService         service.UserService
	serverService       service.ServerService
	subscriptionService service.SubscriptionService
	paymentService      service.PaymentService
	planService         service.PlanService
	faqService          service.FAQService
	instructionService  service.InstructionService

	// Временное хранилище для диалогов (в реальном приложении должно быть в базе данных)
	// ключ - TelegramID пользователя, значение - карта с состоянием диалога
	dialogStates map[int64]map[string]interface{}
}

// New creates and initializes a new Bot instance.
func New(
	cfg *config.Config,
	logger *slog.Logger,
	userService service.UserService,
	serverService service.ServerService,
	subscriptionService service.SubscriptionService,
	paymentService service.PaymentService,
	planService service.PlanService,
	faqService service.FAQService,
	instructionService service.InstructionService,
) (*Bot, error) {
	b := &Bot{
		cfg:                 cfg,
		logger:              logger.With(slog.String("component", "bot")),
		userService:         userService,
		serverService:       serverService,
		subscriptionService: subscriptionService,
		paymentService:      paymentService,
		planService:         planService,
		faqService:          faqService,
		instructionService:  instructionService,
		dialogStates:        make(map[int64]map[string]interface{}),
	}

	opts := []gobot.Option{
		gobot.WithMiddlewares(b.logMiddleware, b.userMiddleware),
		gobot.WithDefaultHandler(b.defaultHandler),
	}

	var err error
	b.api, err = gobot.New(cfg.Telegram.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	// Register command and text handlers (defined in handlers.go)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/start", gobot.MatchTypeExact, b.startHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/subscriptions", gobot.MatchTypeExact, b.mySubscriptionsHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/buy", gobot.MatchTypeExact, b.buySubscriptionHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/referral", gobot.MatchTypeExact, b.referralHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/faq", gobot.MatchTypeExact, b.faqHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/makeadmin", gobot.MatchTypePrefix, b.adminRequired(b.makeAdminHandler))

	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonMySubscriptions, gobot.MatchTypeExact, b.mySubscriptionsHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonBuySubscription, gobot.MatchTypeExact, b.buySubscriptionHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonReferral, gobot.MatchTypeExact, b.referralHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonInstructions, gobot.MatchTypeExact, b.instructionHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonFAQ, gobot.MatchTypeExact, b.faqHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonSupport, gobot.MatchTypeExact, b.supportHandler)

	// Кнопка Админ-панели (защищена middleware)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, MainMenuButtonAdmin, gobot.MatchTypeExact, b.adminRequired(b.adminPanelHandler))

	// Кнопки внутри Админ-панели (защищены middleware)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, AdminMenuButtonServers, gobot.MatchTypeExact, b.adminRequired(b.adminServersHandler))

	// Register callback query handlers (defined in handlers.go)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionSelectPlan, gobot.MatchTypePrefix, b.handlePlanSelectionCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionGetConfig, gobot.MatchTypePrefix, b.handleGetConfigCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionSelectServer, gobot.MatchTypePrefix, b.handleSelectServerCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionConfigureServer, gobot.MatchTypePrefix, b.handleServerSelectionCallback)

	// Register new FAQ/Instruction callback handlers
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionFAQCategory, gobot.MatchTypePrefix, b.handleFAQCategoryCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionFAQQuestion, gobot.MatchTypePrefix, b.handleFAQQuestionCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionInstructionPlatform, gobot.MatchTypePrefix, b.handleInstructionPlatformCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionInstructionDetails, gobot.MatchTypePrefix, b.handleInstructionDetailsCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionBackToFAQ, gobot.MatchTypeExact, b.handleBackToFAQCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionBackToInstructions, gobot.MatchTypeExact, b.handleBackToInstructionsCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionBackToHelpRoot, gobot.MatchTypeExact, b.handleBackToHelpRootCallback)

	// Регистрируем обработчики для callback'ов админки серверов (защищены middleware)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServersActionAdd, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerAddCallback))
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServersActionList, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerListCallback))
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServersBackToAdmin, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerBackCallback))

	// Регистрируем обработчики для диалога добавления сервера
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackServerAddStepCancel, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerCancelAddCallback))
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackServerAddStepConfirm, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerAddConfirmCallback))

	// TODO: Реализовать полноценный механизм отслеживания состояния диалога пользователя
	// через UserService или с помощью контекста. Временно используем обработчик defaultHandler
	// для перехвата текстовых сообщений от администраторов, которые могут быть связаны с диалогом
	// добавления сервера.

	// TODO: Добавить регистрацию обработчиков для callback'ов просмотра/удаления/переключения сервера

	// Note: PreCheckoutQuery and SuccessfulPayment are handled within the defaultHandler

	return b, nil
}

// Start begins polling for updates and handling them using go-telegram/bot handlers
func (b *Bot) Start(ctx context.Context) error {
	b.logger.Info("Starting go-telegram bot polling...")
	// Handlers are registered in New()
	b.api.Start(ctx)
	b.logger.Info("Bot polling stopped.")
	return nil
}

// Stop is handled by context cancellation in Start
func (b *Bot) Stop() {
	b.logger.Info("Bot Stop() called. Polling stopped via context cancellation in Start.")
}

// mainMenuKeyboard is a wrapper method calling the keyboard function.
func (b *Bot) mainMenuKeyboard() models.ReplyKeyboardMarkup {
	// В b.api.Context нет доступа к контексту метода, поэтому используем
	// стандартную клавиатуру без проверки на админа
	return *mainMenuKeyboard()
}

// --- Removed Handlers, Middlewares, Helpers --- //
// The implementations for handlers (startHandler, buySubscriptionHandler, etc.),
// middlewares (logMiddleware, userMiddleware), and helpers (sendUserError,
// sendInternalError, context functions, escapeMarkdownV2, translateStatus)
// have been moved to handlers.go, middleware.go, and helpers.go respectively.

// --- Removed Keyboard Functions --- //
// Implementations for createPlansKeyboard, buildServerSelectionKeyboard etc.
// are now in keyboards.go.

// adminRequired - middleware для проверки прав администратора
// (Если его нет в middleware.go, его нужно создать)
func (b *Bot) adminRequired(next gobot.HandlerFunc) gobot.HandlerFunc {
	return func(ctx context.Context, bot *gobot.Bot, update *models.Update) {
		user := UserFromContext(ctx)
		if user == nil {
			// Попытка отправить сообщение об ошибке, если возможно
			if update.Message != nil {
				b.sendUserError(ctx, bot, update.Message.Chat.ID, "Ошибка: не удалось получить информацию о пользователе.")
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, err := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Ошибка: не удалось получить информацию о пользователе.",
					ShowAlert:       true, // Показываем алерт, т.к. это ошибка
				})
				if err != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (user nil)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", err))
				}
			}
			return
		}

		isAdmin, err := b.userService.IsAdmin(ctx, user.ID)
		if err != nil {
			b.logger.ErrorContext(ctx, "Failed to check admin status in middleware", slog.String("user_id", user.ID.Hex()), slog.Any("error", err))
			if update.Message != nil {
				b.sendInternalError(ctx, bot, update.Message.Chat.ID)
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, errAns := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Внутренняя ошибка сервера.",
					ShowAlert:       true,
				})
				if errAns != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (check error)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", errAns))
				}
			}
			return
		}

		if !isAdmin {
			b.logger.WarnContext(ctx, "Admin access denied", slog.String("user_id", user.ID.Hex()))
			if update.Message != nil {
				b.sendUserMessage(ctx, bot, update.Message.Chat.ID, "⛔ Доступ запрещен. Эта команда доступна только администраторам.")
			} else if update.CallbackQuery != nil {
				// Используем bot.AnswerCallbackQuery
				_, errAns := bot.AnswerCallbackQuery(ctx, &gobot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "⛔ Доступ запрещен.",
					ShowAlert:       true, // Показываем алерт об отказе
				})
				if errAns != nil {
					b.logger.ErrorContext(ctx, "Failed to answer callback query in adminRequired (access denied)", slog.String("callback_query_id", update.CallbackQuery.ID), slog.Any("error", errAns))
				}
			}
			return // Stop processing
		}

		// Если админ, передаем управление следующему обработчику
		next(ctx, bot, update)
	}
}

// TODO: Перенести adminRequired в middleware.go, если его там еще нет.

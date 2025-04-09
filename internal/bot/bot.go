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
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, AdminMenuButtonServers, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServersHandler))

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

	// Регистрируем обработчики для callback'ов управления серверами (просмотр, удаление, переключение)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServerView, gobot.MatchTypePrefix, b.adminRequired(b.handleAdminServerViewCallback))                   // Просмотр деталей сервера
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServerDeleteConfirm, gobot.MatchTypePrefix, b.adminRequired(b.handleAdminServerDeleteConfirmCallback)) // Подтверждение удаления
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServerDeleteCancel, gobot.MatchTypePrefix, b.adminRequired(b.handleAdminServerDeleteCancelCallback))   // Отмена удаления
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServerToggle, gobot.MatchTypePrefix, b.adminRequired(b.handleAdminServerToggleCallback))               // Включить/выключить сервер
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackAdminServerBackToList, gobot.MatchTypeExact, b.adminRequired(b.handleAdminServerBackToListCallback))        // Назад к списку серверов

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

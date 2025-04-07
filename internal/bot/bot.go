package bot

import (
	"context"
	"fmt"
	"log/slog"

	"xray-vpn-tg-bot/internal/config"
	"xray-vpn-tg-bot/internal/service"

	gobot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	// Removed unused imports like errors, strings, time, repository, domain, qrcode, primitive
)

// Callback action prefixes
const (
	callbackActionSelectPlan      = "plan_id:"
	callbackActionGetConfig       = "get_config:"
	callbackActionSelectServer    = "select_srv:"
	callbackActionConfigureServer = "config_srv:"
	callbackActionConfirmServer   = "confirm_srv_"
)

// Bot represents the Telegram bot application using go-telegram/bot
type Bot struct {
	api                 *gobot.Bot
	logger              *slog.Logger
	cfg                 *config.Config
	userService         service.UserService
	serverService       service.ServerService
	subscriptionService service.SubscriptionService
	paymentService      service.PaymentService
	planService         service.PlanService
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
) (*Bot, error) {
	b := &Bot{
		cfg:                 cfg,
		logger:              logger.With(slog.String("component", "bot")),
		userService:         userService,
		serverService:       serverService,
		subscriptionService: subscriptionService,
		paymentService:      paymentService,
		planService:         planService,
	}

	opts := []gobot.Option{
		gobot.WithMiddlewares(b.logMiddleware, b.userMiddleware), // Middlewares are defined in middleware.go
		gobot.WithDefaultHandler(b.defaultHandler),               // Default handler is defined in handlers.go
	}

	var err error
	b.api, err = gobot.New(cfg.Telegram.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	// Register command and text handlers (defined in handlers.go)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "/start", gobot.MatchTypeExact, b.startHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "🚀 Мои подписки", gobot.MatchTypeExact, b.mySubscriptionsHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "🛒 Купить подписку", gobot.MatchTypeExact, b.buySubscriptionHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "🎁 Реферальная программа", gobot.MatchTypeExact, b.referralHandler)
	b.api.RegisterHandler(gobot.HandlerTypeMessageText, "❓ FAQ & Поддержка", gobot.MatchTypeExact, b.faqHandler)

	// Register callback query handlers (defined in handlers.go)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionSelectPlan, gobot.MatchTypePrefix, b.handlePlanSelectionCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionGetConfig, gobot.MatchTypePrefix, b.handleGetConfigCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionSelectServer, gobot.MatchTypePrefix, b.handleSelectServerCallback)
	b.api.RegisterHandler(gobot.HandlerTypeCallbackQueryData, callbackActionConfigureServer, gobot.MatchTypePrefix, b.handleServerSelectionCallback)

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
	// This method remains on Bot, but calls the actual keyboard generation function
	// which is now in keyboards.go
	return *mainMenuKeyboard() // Calls the function defined in keyboards.go
}

// --- Removed Handlers, Middlewares, Helpers --- //
// The implementations for handlers (startHandler, buySubscriptionHandler, etc.),
// middlewares (logMiddleware, userMiddleware), and helpers (sendUserError,
// sendInternalError, context functions, escapeMarkdownV2, translateStatus)
// have been moved to handlers.go, middleware.go, and helpers.go respectively.

// --- Removed Keyboard Functions --- //
// Implementations for createPlansKeyboard, buildServerSelectionKeyboard etc.
// are now in keyboards.go.

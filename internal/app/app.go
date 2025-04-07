package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"xray-vpn-tg-bot/internal/bot"
	"xray-vpn-tg-bot/internal/config"
	"xray-vpn-tg-bot/internal/delivery/http/handler"
	mw "xray-vpn-tg-bot/internal/delivery/http/middleware"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
	"xray-vpn-tg-bot/internal/repository/mongodb"
	"xray-vpn-tg-bot/internal/service"
	"xray-vpn-tg-bot/internal/xui"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultShutdownTimeout = 15 * time.Second
)

// App represents the main application
type App struct {
	logger      *slog.Logger
	cfg         *config.Config
	httpServer  *http.Server
	tgBot       *bot.Bot
	mongoClient *mongodriver.Client
	// Add other components (e.g., task scheduler)
}

// New creates and initializes a new App instance
func New(ctx context.Context, logger *slog.Logger, cfg *config.Config) (*App, error) {
	logger.Info("Initializing application components...")

	// --- Initialize Database (MongoDB) ---
	logger.Info("Connecting to MongoDB...")
	mongoClient, err := setupMongoDB(ctx, cfg.MongoDB)
	if err != nil {
		return nil, fmt.Errorf("failed to setup MongoDB: %w", err)
	}
	mongoDB := mongoClient.Database(cfg.MongoDB.DBName)
	logger.Info("MongoDB connected successfully", slog.String("db_name", cfg.MongoDB.DBName))

	// --- Initialize Repositories ---
	logger.Info("Initializing repositories...")
	userMongoRepo := mongodb.NewUserRepo(mongoDB, cfg.MongoDB.UserColl, logger)
	if repoWithIndexes, ok := userMongoRepo.(*mongodb.UserRepo); ok {
		if err := repoWithIndexes.EnsureIndexes(ctx); err != nil {
			logger.Error("Failed to ensure user indexes", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("User repository does not support EnsureIndexes method")
	}
	var userRepo repository.UserRepository = userMongoRepo

	serverMongoRepo := mongodb.NewServerRepo(mongoDB, cfg.MongoDB.ServerColl, logger)
	if repoWithIndexes, ok := serverMongoRepo.(*mongodb.ServerRepo); ok {
		if err := repoWithIndexes.EnsureIndexes(ctx); err != nil {
			logger.Error("Failed to ensure server indexes", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("Server repository does not support EnsureIndexes method")
	}
	var serverRepo repository.ServerRepository = serverMongoRepo

	planMongoRepo := mongodb.NewPlanRepo(mongoDB, cfg.MongoDB.PlanColl, logger)
	if repoWithIndexes, ok := planMongoRepo.(*mongodb.PlanRepo); ok {
		if err := repoWithIndexes.EnsureIndexes(ctx); err != nil {
			logger.Error("Failed to ensure plan indexes", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("Plan repository does not support EnsureIndexes method")
	}
	var planRepo repository.PlanRepository = planMongoRepo

	subMongoRepo := mongodb.NewSubscriptionRepo(mongoDB, cfg.MongoDB.SubscriptionColl, logger)
	if repoWithIndexes, ok := subMongoRepo.(*mongodb.SubscriptionRepo); ok {
		if err := repoWithIndexes.EnsureIndexes(ctx); err != nil {
			logger.Error("Failed to ensure subscription indexes", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("Subscription repository does not support EnsureIndexes method")
	}
	var subRepo repository.SubscriptionRepository = subMongoRepo

	paymentMongoRepo := mongodb.NewPaymentRepo(mongoDB, cfg.MongoDB.PaymentColl, logger)
	if repoWithIndexes, ok := paymentMongoRepo.(*mongodb.PaymentRepo); ok {
		if err := repoWithIndexes.EnsureIndexes(ctx); err != nil {
			logger.Error("Failed to ensure payment indexes", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("Payment repository does not support EnsureIndexes method")
	}
	var paymentRepo repository.PaymentRepository = paymentMongoRepo

	// Initialize FAQ and Instruction Repositories
	faqMongoRepo := mongodb.NewFAQRepo(mongoDB, cfg.MongoDB.FaqColl, logger)
	// No EnsureIndexes for FAQ repo currently
	var faqRepo repository.FAQRepository = faqMongoRepo

	instructionMongoRepo := mongodb.NewInstructionRepo(mongoDB, cfg.MongoDB.InstructionColl, logger)
	// No EnsureIndexes for Instruction repo currently
	var instructionRepo repository.InstructionRepository = instructionMongoRepo
	logger.Info("Repositories initialized")

	// --- Initialize Services ---
	logger.Info("Initializing services...")
	userService := service.NewUserService(userRepo, logger)
	serverService := service.NewServerService(serverRepo, logger, domain.XUIConfig{APITimeout: cfg.XUI.APITimeout})
	xuiClientFactory := func(server *domain.Server) (*xui.Client, error) {
		return xui.NewClient(server.ApiHost, server.ApiUsername, server.ApiPassword, cfg.XUI.APITimeout, logger)
	}
	subscriptionService := service.NewSubscriptionService(subRepo, planRepo, serverRepo, userService, xuiClientFactory, logger)

	// Initialize Payment Service (now requires SubscriptionService)
	paymentService := service.NewPaymentService(
		paymentRepo,
		planRepo,
		subRepo,
		userRepo,
		subscriptionService,
		logger,
	)
	// Initialize Plan Service
	planService := service.NewPlanService(planRepo, logger)

	// Initialize FAQ and Instruction Services
	faqService := service.NewFAQService(faqRepo, logger)
	instructionService := service.NewInstructionService(instructionRepo, logger)
	logger.Info("Services initialized")

	// --- Initialize Telegram Bot ---
	logger.Info("Initializing Telegram Bot...")
	tgBot, err := bot.New(cfg, logger, userService, serverService, subscriptionService, paymentService, planService, faqService, instructionService)
	if err != nil {
		// Clean up MongoDB connection if bot fails to start
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if mongoErr := mongoClient.Disconnect(disconnectCtx); mongoErr != nil {
			logger.Error("Failed to disconnect MongoDB during bot init cleanup", slog.String("error", mongoErr.Error()))
		}
		return nil, fmt.Errorf("failed to initialize Telegram Bot: %w", err)
	}
	logger.Info("Telegram Bot initialized")

	// --- Initialize HTTP Server & API Handlers ---
	logger.Info("Initializing HTTP Server...")
	apiRouter := setupRouter(logger, cfg, userService, serverService, subscriptionService)
	httpServer := &http.Server{
		Addr:         cfg.HTTPServer.Address,
		Handler:      apiRouter,
		ReadTimeout:  cfg.HTTPServer.Timeout,
		WriteTimeout: cfg.HTTPServer.Timeout,
		IdleTimeout:  cfg.HTTPServer.IdleTimeout,
	}
	logger.Info("HTTP Server initialized", slog.String("address", cfg.HTTPServer.Address))

	return &App{
		logger:      logger,
		cfg:         cfg,
		httpServer:  httpServer,
		tgBot:       tgBot,
		mongoClient: mongoClient,
	}, nil
}

// Run starts all application components (HTTP server, Telegram bot polling) and waits for context cancellation.
func (a *App) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	errChan := make(chan error, 2) // Buffered channel for errors from goroutines

	// Start HTTP Server
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.logger.Info("Starting HTTP server...", slog.String("addr", a.httpServer.Addr))
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("HTTP server ListenAndServe error", slog.String("error", err.Error()))
			errChan <- fmt.Errorf("http server error: %w", err)
		} else {
			a.logger.Info("HTTP server stopped listening.")
		}
	}()

	// Start Telegram Bot Polling
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.logger.Info("Starting Telegram bot polling...")
		if err := a.tgBot.Start(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			a.logger.Error("Telegram bot Start error", slog.String("error", err.Error()))
			errChan <- fmt.Errorf("telegram bot error: %w", err)
		} else {
			a.logger.Info("Telegram bot polling stopped gracefully.")
		}
	}()

	// Wait for context cancellation or an error from a component
	var runErr error
	select {
	case err := <-errChan:
		a.logger.Error("Received error from component, initiating shutdown...", slog.String("error", err.Error()))
		runErr = err // Store the error that caused the shutdown
		// Don't cancel context here, let the main context handle it or shutdown proceed
	case <-ctx.Done():
		a.logger.Info("Context cancelled, initiating graceful shutdown...")
		runErr = ctx.Err() // Store context error (e.g., context.Canceled)
	}

	// Initiate graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	a.shutdown(shutdownCtx)

	// Wait for all background goroutines (server, bot) to finish
	wg.Wait()
	a.logger.Info("All application components stopped.")

	return runErr // Return the error that initiated the shutdown (if any)
}

// shutdown gracefully stops the application components
func (a *App) shutdown(ctx context.Context) {
	a.logger.Info("Starting graceful shutdown...")

	// Stop Telegram Bot polling first (it's stopped by context cancellation in Start)
	if a.tgBot != nil {
		a.logger.Info("Telegram bot stop signal sent via context.")
	}

	// Shutdown HTTP server (allows active connections to finish)
	if a.httpServer != nil {
		a.logger.Info("Shutting down HTTP server...")
		if err := a.httpServer.Shutdown(ctx); err != nil {
			a.logger.Error("HTTP server shutdown error", slog.String("error", err.Error()))
		} else {
			a.logger.Info("HTTP server shut down successfully.")
		}
	}

	// Disconnect MongoDB
	if a.mongoClient != nil {
		a.logger.Info("Disconnecting from MongoDB...")
		if err := a.mongoClient.Disconnect(ctx); err != nil {
			a.logger.Error("MongoDB disconnection error", slog.String("error", err.Error()))
		} else {
			a.logger.Info("MongoDB disconnected successfully.")
		}
	}

	a.logger.Info("Graceful shutdown completed.")
}

// setupMongoDB initializes the MongoDB client
func setupMongoDB(ctx context.Context, cfg config.MongoDB) (*mongodriver.Client, error) {
	// Set a connection timeout
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	clientOptions := options.Client().ApplyURI(cfg.URI)
	client, err := mongodriver.Connect(connectCtx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo: %w", err)
	}

	// Ping the primary with a timeout
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := client.Ping(pingCtx, nil); err != nil {
		// Disconnect if ping fails
		disconnectCtx, cancelDisconnect := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDisconnect()
		_ = client.Disconnect(disconnectCtx)
		return nil, fmt.Errorf("failed to ping mongo: %w", err)
	}

	return client, nil
}

// setupRouter configures the HTTP router
// Removed paymentService argument
func setupRouter(logger *slog.Logger, cfg *config.Config, userService service.UserService, serverService service.ServerService, subscriptionService service.SubscriptionService) *chi.Mux {
	r := chi.NewRouter()

	// Middlewares
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(mw.SlogMiddleware(logger))
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	// --- Public Routes ---
	r.Get("/health", handler.HealthCheck(logger))

	// --- API v1 Routes ---
	r.Route("/api/v1", func(r chi.Router) {
		// User routes
		usersHandler := handler.NewUsersHandler(userService, logger)
		r.Mount("/users", usersHandler.Routes())

		// Server routes
		serversHandler := handler.NewServersHandler(serverService, logger)
		r.Mount("/servers", serversHandler.Routes())

		// Subscription routes
		subscriptionsHandler := handler.NewSubscriptionsHandler(subscriptionService, logger)
		r.Mount("/subscriptions", subscriptionsHandler.Routes())

		// Plan routes (placeholder)
		// plansHandler := handler.NewPlansHandler(planService, logger)
		// r.Mount("/plans", plansHandler.Routes())
	})

	// --- Webhook Routes (Removed YooKassa handler) ---
	// r.Route("/webhooks", func(r chi.Router) {
	// 	if paymentService != nil { ... } // Removed
	// })

	logger.Info("Router configured")
	return r
}

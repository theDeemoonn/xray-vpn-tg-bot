package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"xray-vpn-tg-bot/internal/app"
	"xray-vpn-tg-bot/internal/config"
)

func main() {
	// Setup logger
	logger := setupLogger()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("Starting application", slog.String("env", cfg.Env))

	// Create application context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create and run the application
	application, err := app.New(ctx, logger, cfg)
	if err != nil {
		logger.Error("Failed to initialize application", slog.String("error", err.Error()))
		os.Exit(1)
	}

	err = application.Run(ctx)
	if err != nil {
		logger.Error("Application run failed", slog.String("error", err.Error()))
		// No os.Exit(1) here, allow graceful shutdown initiated by Run failure
	}

	logger.Info("Application stopped")
}

func setupLogger() *slog.Logger {
	// Customize logger level and format based on config if needed
	// For now, using a simple text handler with debug level
	opts := &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true, // Include source file and line number
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

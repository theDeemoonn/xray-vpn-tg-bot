package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"xray-vpn-tg-bot/internal/config"
	"xray-vpn-tg-bot/internal/domain"
)

func main() {
	logger := setupLogger()
	logger.Info("Starting database seeder...")

	// Load configuration (uses .env or environment variables)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create context with timeout for connection
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // Increased timeout for potential seeding
	defer cancel()

	// Connect to MongoDB
	mongoClient, err := setupMongoDB(ctx, cfg.MongoDB, logger)
	if err != nil {
		logger.Error("Failed to connect to MongoDB", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		logger.Info("Disconnecting MongoDB...")
		disconnectCtx, cancelDisconnect := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelDisconnect()
		if err := mongoClient.Disconnect(disconnectCtx); err != nil {
			logger.Error("Failed to disconnect MongoDB", slog.String("error", err.Error()))
		}
	}()

	db := mongoClient.Database(cfg.MongoDB.DBName)

	// --- Seed Data ---
	// Use a new context for seeding operations themselves
	seedCtx, stopSeeder := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSeeder()

	logger.Info("Seeding Plans...")
	if err := seedPlans(seedCtx, db.Collection(cfg.MongoDB.PlanColl), logger); err != nil {
		logger.Error("Failed to seed Plans", slog.Any("error", err))
		// Decide if we should exit or continue with other collections
	} else {
		logger.Info("Plans seeded successfully.")
	}

	logger.Info("Seeding FAQs...")
	if err := seedFAQs(seedCtx, db.Collection(cfg.MongoDB.FaqColl), logger); err != nil {
		logger.Error("Failed to seed FAQs", slog.Any("error", err))
	} else {
		logger.Info("FAQs seeded successfully.")
	}

	logger.Info("Seeding Instructions...")
	if err := seedInstructions(seedCtx, db.Collection(cfg.MongoDB.InstructionColl), logger); err != nil {
		logger.Error("Failed to seed Instructions", slog.Any("error", err))
	} else {
		logger.Info("Instructions seeded successfully.")
	}

	logger.Info("Database seeding finished.")
}

func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

func setupMongoDB(ctx context.Context, cfg config.MongoDB, logger *slog.Logger) (*mongo.Client, error) {
	clientOptions := options.Client().ApplyURI(cfg.URI)

	logger.Info("Attempting to connect to MongoDB", slog.String("uri", cfg.URI)) // Don't log full URI in production if it contains creds

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo: %w", err)
	}

	// Ping the primary
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		// Disconnect if ping fails
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(disconnectCtx) // Ignore disconnect error after ping failure
		return nil, fmt.Errorf("failed to ping mongo primary: %w", err)
	}

	logger.Info("MongoDB connection established successfully.")
	return client, nil
}

// --- Seeding Functions ---

func seedPlans(ctx context.Context, collection *mongo.Collection, logger *slog.Logger) error {
	now := time.Now()
	plans := []domain.Plan{
		{
			Name:      "Стандарт 1 час",
			Price:     70.00,
			Currency:  "RUB",
			Duration:  time.Hour * 1, // 1 час
			TrafficGB: 3,             // 3 GB Limit
			IsActive:  true,
			SortOrder: 1,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Стандарт 3 часа",
			Price:     85.00,
			Currency:  "RUB",
			Duration:  time.Hour * 3, // 3 часа
			TrafficGB: 5,             // 5 GB Limit
			IsActive:  true,
			SortOrder: 2,
			CreatedAt: now,
			UpdatedAt: now,
		},

		{
			Name:      "Стандарт 1 день",
			Price:     120.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24, // 1 день
			TrafficGB: 10,             // 10 GB Limit
			IsActive:  true,
			SortOrder: 4,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Стандарт 1 неделя",
			Price:     190.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 7, // 1 неделя
			TrafficGB: 40,                 // 40 GB Limit
			IsActive:  true,
			SortOrder: 6,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Стандарт 30 дней",
			Price:     220.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 30, // Use time.Duration
			TrafficGB: 50,                  // 50 GB Limit
			IsActive:  true,
			SortOrder: 7,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Безлимит 30 дней",
			Price:     300.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 30, // Use time.Duration
			TrafficGB: 0,                   // Unlimited
			IsActive:  true,
			SortOrder: 8,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Стандарт 90 дней",
			Price:     400.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 90, // Use time.Duration
			TrafficGB: 150,                 // 150 GB Limit
			IsActive:  true,
			SortOrder: 9,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Безлимит 90 дней",
			Price:     600.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 90, // 3 месяца
			TrafficGB: 0,                   // Unlimited
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Стандарт 1 год",
			Price:     1900.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 365, // 1 год
			TrafficGB: 500,                  // 500 GB Limit
			IsActive:  true,
			SortOrder: 11,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:      "Безлимит 1 год",
			Price:     2900.00,
			Currency:  "RUB",
			Duration:  time.Hour * 24 * 365, // 1 год
			TrafficGB: 0,                    // Unlimited
			IsActive:  true,
			SortOrder: 12,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	// Convert slice of structs to slice of interfaces for InsertMany
	documents := make([]interface{}, len(plans))
	for i, p := range plans {
		// Ensure ID is zero so MongoDB generates it (using omitempty)
		p.ID = primitive.NilObjectID
		documents[i] = p
	}

	// Optional: Check if collection is empty before inserting
	count, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		logger.WarnContext(ctx, "Could not count documents in plans collection", slog.Any("error", err))
		// Continue anyway, InsertMany might fail if duplicates exist
	}

	if count > 0 {
		logger.WarnContext(ctx, "Plans collection is not empty. Skipping seeding to avoid duplicates.", slog.Int64("count", count))
		return nil // Or return an error if seeding should only happen on empty collections
	}

	if len(documents) > 0 {
		res, err := collection.InsertMany(ctx, documents)
		if err != nil {
			return fmt.Errorf("failed to insert plans: %w", err)
		}
		logger.DebugContext(ctx, "Inserted plans", slog.Int("count", len(res.InsertedIDs)))
	} else {
		logger.InfoContext(ctx, "No plans defined to seed.")
	}

	return nil
}

func seedFAQs(ctx context.Context, collection *mongo.Collection, logger *slog.Logger) error {
	now := time.Now()
	faqs := []domain.FAQ{
		{
			Category:  "Оплата",
			Question:  "Какие способы оплаты доступны?",
			Answer:    "Мы принимаем оплату через YooKassa (банковские карты РФ, SberPay, Tinkoff Pay и др.).\nДля других стран доступна оплата через Telegram Stars.",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Category:  "Оплата",
			Question:  "Как продлить подписку?",
			Answer:    "Для продления подписки просто купите новый тарифный план через кнопку '🛒 Купить подписку'.\nНовая подписка начнется после окончания текущей.",
			IsActive:  true,
			SortOrder: 20,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Category:  "Технические вопросы",
			Question:  "VPN не подключается, что делать?",
			Answer:    "1. Убедитесь, что ваша подписка активна ('🚀 Мои подписки').\n2. Проверьте правильность конфигурации в вашем VPN-клиенте.\n3. Попробуйте получить конфигурацию заново ('🔗 Получить конфигурацию').\n4. Перезагрузите устройство и роутер.\n5. Если проблема сохраняется, свяжитесь с поддержкой ('💬 Поддержка').",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Category:  "Технические вопросы",
			Question:  "Низкая скорость соединения",
			Answer:    "Скорость VPN зависит от множества факторов: загруженности сервера, вашего интернет-провайдера, качества вашего соединения.\nПопробуйте подключиться к другому серверу (если доступно) или проверьте скорость вашего интернета без VPN.",
			IsActive:  true,
			SortOrder: 20,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Category:  "Общие вопросы",
			Question:  "Можно ли использовать VPN на нескольких устройствах?",
			Answer:    "Да, вы можете использовать одну конфигурацию на нескольких устройствах одновременно.\nОднако, обратите внимание на лимит трафика вашего тарифного плана, если он не безлимитный.",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	documents := make([]interface{}, len(faqs))
	for i, f := range faqs {
		f.ID = primitive.NilObjectID // Ensure MongoDB generates ID
		documents[i] = f
	}

	count, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		logger.WarnContext(ctx, "Could not count documents in faqs collection", slog.Any("error", err))
	}
	if count > 0 {
		logger.WarnContext(ctx, "FAQs collection is not empty. Skipping seeding.", slog.Int64("count", count))
		return nil
	}

	if len(documents) > 0 {
		res, err := collection.InsertMany(ctx, documents)
		if err != nil {
			return fmt.Errorf("failed to insert faqs: %w", err)
		}
		logger.DebugContext(ctx, "Inserted FAQs", slog.Int("count", len(res.InsertedIDs)))
	} else {
		logger.InfoContext(ctx, "No FAQs defined to seed.")
	}

	return nil
}

func seedInstructions(ctx context.Context, collection *mongo.Collection, logger *slog.Logger) error {
	now := time.Now()
	instructions := []domain.Instruction{
		{
			Title:     "V2RayNG (Android)",
			Content:   "1. Скопируйте ссылку на конфигурацию (начинается с 'vless://...').\n2. Откройте V2RayNG.\n3. Нажмите на '+' в правом верхнем углу.\n4. Выберите 'Import config from Clipboard'.\n5. Нажмите на серый кружок V внизу справа для подключения.",
			Platform:  "Android",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Title:     "Streisand (iOS)",
			Content:   "1. Скопируйте ссылку на конфигурацию (начинается с 'vless://...').\n2. Откройте Streisand.\n3. Нажмите на '+' в правом верхнем углу.\n4. Выберите 'Добавить подписку'.\n5. Вставьте скопированную ссылку в поле 'URL' и нажмите 'ОК'.\n6. Перейдите на вкладку 'Главная', выберите добавленную подписку и нажмите кнопку подключения.",
			Platform:  "iOS",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Title:     "Shadowrocket (iOS)",
			Content:   "1. Скопируйте ссылку на конфигурацию (начинается с 'vless://...').\n2. Откройте Shadowrocket.\n3. Нажмите на '+' в левом верхнем углу.\n4. В поле 'Type' выберите 'Subscribe'.\n5. В поле 'URL' вставьте скопированную ссылку.\n6. Нажмите 'Done'.\n7. На главном экране выберите добавленную подписку и сервер, затем активируйте переключатель.",
			Platform:  "iOS",
			IsActive:  true,
			SortOrder: 20,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Title:     "NekoBox / NekoRay (Windows/Linux)",
			Content:   "1. Скопируйте ссылку на конфигурацию (начинается с 'vless://...').\n2. Откройте NekoBox/NekoRay.\n3. В главном окне нажмите Ctrl+V, чтобы импортировать конфигурацию из буфера обмена.\n4. Убедитесь, что выбран режим 'VPN Mode' или 'Tun Mode' (в зависимости от вашей системы).\n5. Выберите импортированный профиль и нажмите Enter или кнопку подключения.",
			Platform:  "Windows/Linux",
			IsActive:  true,
			SortOrder: 10,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Title:     "Как получить ссылку?",
			Content:   "1. Убедитесь, что у вас есть активная подписка ('🚀 Мои подписки').\n2. Если подписка активна, но не настроена, нажмите '⚙️ Настроить сервер' и выберите сервер.\n3. После выбора сервера или если он уже был выбран, нажмите '🔗 Получить конфигурацию'.\n4. Бот пришлет вам QR-код и текстовую ссылку.",
			Platform:  "Общее", // General category
			IsActive:  true,
			SortOrder: 1, // Show first in general
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	documents := make([]interface{}, len(instructions))
	for i, ins := range instructions {
		ins.ID = primitive.NilObjectID // Ensure MongoDB generates ID
		documents[i] = ins
	}

	count, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		logger.WarnContext(ctx, "Could not count documents in instructions collection", slog.Any("error", err))
	}
	if count > 0 {
		logger.WarnContext(ctx, "Instructions collection is not empty. Skipping seeding.", slog.Int64("count", count))
		return nil
	}

	if len(documents) > 0 {
		res, err := collection.InsertMany(ctx, documents)
		if err != nil {
			return fmt.Errorf("failed to insert instructions: %w", err)
		}
		logger.DebugContext(ctx, "Inserted instructions", slog.Int("count", len(res.InsertedIDs)))
	} else {
		logger.InfoContext(ctx, "No instructions defined to seed.")
	}

	return nil
}

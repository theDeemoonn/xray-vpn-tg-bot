package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

type Config struct {
	Env        string `yaml:"env" env:"ENV" env-default:"local"`
	HTTPServer `yaml:"http_server"`
	Telegram   `yaml:"telegram"`
	MongoDB    `yaml:"mongodb"`
	XUI        `yaml:"xui"`
	YooKassa   `yaml:"yookassa"`
}

type HTTPServer struct {
	Address     string        `yaml:"address" env:"HTTP_ADDRESS" env-default:"localhost:8080"`
	Timeout     time.Duration `yaml:"timeout" env:"HTTP_TIMEOUT" env-default:"5s"`
	IdleTimeout time.Duration `yaml:"idle_timeout" env:"HTTP_IDLE_TIMEOUT" env-default:"60s"`
}

type Telegram struct {
	Token         string `yaml:"token" env:"TELEGRAM_TOKEN" env-required:"true"`
	WebhookURL    string `yaml:"webhook_url" env:"TELEGRAM_WEBHOOK_URL"`                           // Если используем вебхуки
	AdminID       int64  `yaml:"admin_id" env:"TELEGRAM_ADMIN_ID"`                                 // ID админа для уведомлений
	ProviderToken string `yaml:"provider_token" env:"TELEGRAM_PROVIDER_TOKEN" env-required:"true"` // Added for Telegram Payments
}

type MongoDB struct {
	URI              string `yaml:"uri" env:"MONGO_URI" env-required:"true"`
	DBName           string `yaml:"db_name" env:"MONGO_DB_NAME" env-default:"xray_vpn_bot"`
	UserColl         string `yaml:"user_collection" env:"MONGO_USER_COLL" env-default:"users"`
	ServerColl       string `yaml:"server_collection" env:"MONGO_SERVER_COLL" env-default:"servers"`
	PlanColl         string `yaml:"plan_collection" env:"MONGO_PLAN_COLL" env-default:"plans"`
	SubscriptionColl string `yaml:"subscription_collection" env:"MONGO_SUBSCRIPTION_COLL" env-default:"subscriptions"`
	PaymentColl      string `yaml:"payment_collection" env:"MONGO_PAYMENT_COLL" env-default:"payments"`
	ReferralColl     string `yaml:"referral_collection" env:"MONGO_REFERRAL_COLL" env-default:"referrals"`
	FaqColl          string `yaml:"faq_collection" env:"MONGO_FAQ_COLL" env-default:"faqs"`
	InstructionColl  string `yaml:"instruction_collection" env:"MONGO_INSTRUCTION_COLL" env-default:"instructions"`
}

type XUI struct {
	APITimeout time.Duration `yaml:"api_timeout" env:"XUI_API_TIMEOUT" env-default:"10s"`
}

type YooKassa struct {
	ShopID        string `yaml:"shop_id" env:"YOOKASSA_SHOP_ID"`
	SecretKey     string `yaml:"secret_key" env:"YOOKASSA_SECRET_KEY"`
	ReturnURL     string `yaml:"return_url" env:"YOOKASSA_RETURN_URL"`
	WebhookSecret string `yaml:"webhook_secret" env:"YOOKASSA_WEBHOOK_SECRET"`
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/local.yaml"
	}

	var cfg Config

	if _, err := os.Stat(configPath); err == nil {
		err = cleanenv.ReadConfig(configPath, &cfg)
		if err != nil {
			return nil, fmt.Errorf("cannot read config file %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("error checking config file %s: %w", configPath, err)
	} else {
		slog.Warn("Config file not found, loading from environment variables only", "path", configPath)
	}

	err := cleanenv.ReadEnv(&cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot read environment variables: %w", err)
	}

	if cfg.Telegram.Token == "" {
		return nil, fmt.Errorf("telegram token (TELEGRAM_TOKEN or yaml: telegram.token) is required")
	}
	if cfg.MongoDB.URI == "" {
		return nil, fmt.Errorf("mongodb uri (MONGO_URI or yaml: mongodb.uri) is required")
	}

	slog.Info("Configuration loaded successfully", slog.String("env", cfg.Env))

	return &cfg, nil
}

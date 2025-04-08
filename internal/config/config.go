package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

// HTTPServer defines HTTP server configuration
type HTTPServer struct {
	Address     string        `yaml:"address" env:"HTTP_ADDR" env-default:":8080"`
	Timeout     time.Duration `yaml:"timeout" env:"HTTP_TIMEOUT" env-default:"30s"`
	IdleTimeout time.Duration `yaml:"idle_timeout" env:"HTTP_IDLE_TIMEOUT" env-default:"120s"`
}

// Workers содержит конфигурацию для фоновых процессов
type Workers struct {
	SubscriptionCheckInterval time.Duration `yaml:"subscription_check_interval" env:"WORKER_SUB_CHECK_INTERVAL" env-default:"1h"`
}

// Config represents the application configuration
type Config struct {
	LogLevel    string `yaml:"log_level" env:"LOG_LEVEL" env-default:"info"`
	Environment string `yaml:"environment" env:"ENVIRONMENT" env-default:"development"`

	Telegram   Telegram   `yaml:"telegram"`
	MongoDB    MongoDB    `yaml:"mongodb"`
	HTTPServer HTTPServer `yaml:"http_server"`
	XUI        XUI        `yaml:"xui"`
	YooKassa   YooKassa   `yaml:"yookassa"`
	Workers    Workers    `yaml:"workers"`
}

type Telegram struct {
	Token         string  `yaml:"token" env:"TELEGRAM_TOKEN" env-required:"true"`
	WebhookURL    string  `yaml:"webhook_url" env:"TELEGRAM_WEBHOOK_URL"`
	AdminID       int64   `yaml:"admin_id" env:"TELEGRAM_ADMIN_ID"`
	AdminUsername string  `yaml:"admin_username" env:"TELEGRAM_ADMIN_USERNAME"`
	ProviderToken string  `yaml:"provider_token" env:"TELEGRAM_PROVIDER_TOKEN" env-required:"true"`
	AdminIDs      []int64 `yaml:"admin_ids"`
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
	Address  string        `yaml:"address" env:"XUI_ADDRESS" env-required:"true"`
	Username string        `yaml:"username" env:"XUI_USERNAME" env-required:"true"`
	Password string        `yaml:"password" env:"XUI_PASSWORD" env-required:"true"`
	Timeout  time.Duration `yaml:"timeout" env:"XUI_API_TIMEOUT" env-default:"15s"`
}

type YooKassa struct {
	ShopID        string `yaml:"shop_id" env:"YOOKASSA_SHOP_ID"`
	SecretKey     string `yaml:"secret_key" env:"YOOKASSA_SECRET_KEY"`
	ReturnURL     string `yaml:"return_url" env:"YOOKASSA_RETURN_URL"`
	WebhookSecret string `yaml:"webhook_secret" env:"YOOKASSA_WEBHOOK_SECRET"`
}

var (
	cfg  *Config
	once sync.Once
)

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
		log.Println("Config file not found, loading from environment variables only", "path", configPath)
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

	log.Println("Configuration loaded successfully", "env", cfg.Environment)

	return &cfg, nil
}

func MustLoad() *Config {
	once.Do(func() {
		_ = godotenv.Load()

		configPath := os.Getenv("CONFIG_PATH")
		if configPath == "" {
			configPath = "config/config.yml"
		}

		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			log.Fatalf("config file does not exist: %s", configPath)
		}

		var tempCfg Config

		if err := cleanenv.ReadConfig(configPath, &tempCfg); err != nil {
			log.Printf("warning: cannot fully read config %s: %s. Will rely on env vars.", configPath, err)
		}

		if err := cleanenv.ReadEnv(&tempCfg); err != nil {
			log.Fatalf("cannot read env variables: %s", err)
		}

		adminIDsStr := os.Getenv("TELEGRAM_ADMIN_ID")
		if adminIDsStr != "" {
			ids := strings.Split(adminIDsStr, ",")
			tempCfg.Telegram.AdminIDs = make([]int64, 0, len(ids))
			for _, idStr := range ids {
				id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
				if err != nil {
					log.Printf("warning: invalid admin ID '%s' in TELEGRAM_ADMIN_ID: %v", idStr, err)
					continue
				}
				tempCfg.Telegram.AdminIDs = append(tempCfg.Telegram.AdminIDs, id)
			}
		} else if len(tempCfg.Telegram.AdminIDs) == 0 {
			log.Println("warning: TELEGRAM_ADMIN_ID is not set in environment variables or config file")
		}

		cfg = &tempCfg
	})

	return cfg
}

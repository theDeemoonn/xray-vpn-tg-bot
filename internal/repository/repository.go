package repository

import (
	"context"
	"errors"
	"time"

	"xray-vpn-tg-bot/internal/domain"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Common repository errors
var (
	ErrNotFound     = errors.New("entity not found")
	ErrUserExists   = errors.New("user already exists")
	ErrServerExists = errors.New("server with this name or API URL already exists")
	// Add other common errors like ErrUpdateConflict, etc.
)

// UserRepository defines the interface for user data storage operations.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.User, error)
	GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
	// Add other methods as needed (e.g., Delete, GetByReferralCode)
}

// ServerRepository defines the interface for server data storage operations.
type ServerRepository interface {
	Create(ctx context.Context, server *domain.Server) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Server, error)
	GetAllEnabled(ctx context.Context) ([]*domain.Server, error)
	GetAll(ctx context.Context) ([]*domain.Server, error)
	Update(ctx context.Context, server *domain.Server) error
	Delete(ctx context.Context, id primitive.ObjectID) error
	// Add methods like GetByName, GetByApiURL if needed
}

// PlanRepository defines the interface for plan data storage operations.
type PlanRepository interface {
	Create(ctx context.Context, plan *domain.Plan) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Plan, error)
	GetAllActive(ctx context.Context) ([]*domain.Plan, error)
	Update(ctx context.Context, plan *domain.Plan) error
	Delete(ctx context.Context, id primitive.ObjectID) error
}

// SubscriptionRepository defines the interface for subscription data storage operations.
type SubscriptionRepository interface {
	Create(ctx context.Context, sub *domain.Subscription) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Subscription, error)
	GetActiveByUserID(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error) // Assuming one active sub per user
	GetActiveByUserIDAndServerID(ctx context.Context, userID, serverID primitive.ObjectID) (*domain.Subscription, error)
	ListByUserID(ctx context.Context, userID primitive.ObjectID) ([]*domain.Subscription, error)
	Update(ctx context.Context, sub *domain.Subscription) error
	FindToExpire(ctx context.Context, within time.Duration) ([]*domain.Subscription, error) // Find subs expiring soon
	FindActiveNeedingRenewal(ctx context.Context) ([]*domain.Subscription, error)           // Find active subs with auto-renew
	FindByFilter(ctx context.Context, filter interface{}) ([]*domain.Subscription, error)
}

// PaymentRepository defines the interface for payment data storage operations.
type PaymentRepository interface {
	Create(ctx context.Context, payment *domain.Payment) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Payment, error)
	GetByProviderPaymentID(ctx context.Context, provider domain.PaymentProvider, providerID string) (*domain.Payment, error)
	Update(ctx context.Context, payment *domain.Payment) error
	// Add other methods if needed (e.g., ListByUserID)
}

// FAQRepository defines the interface for FAQ data storage operations.
type FAQRepository interface {
	GetAllActiveCategories(ctx context.Context) ([]string, error)
	GetActiveByCategory(ctx context.Context, category string) ([]*domain.FAQ, error)
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.FAQ, error) // Optional, might be useful
	// Add Admin methods later if needed (Create, Update, Delete)
}

// InstructionRepository defines the interface for instruction data storage operations.
type InstructionRepository interface {
	GetAllActivePlatforms(ctx context.Context) ([]string, error)
	GetActiveByPlatform(ctx context.Context, platform string) ([]*domain.Instruction, error)
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Instruction, error) // Optional
	// Add Admin methods later if needed
}

// Add interfaces for other repositories (ReferralRepository, etc.) here

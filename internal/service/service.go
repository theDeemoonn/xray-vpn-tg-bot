package service

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"xray-vpn-tg-bot/internal/domain"
)

// FAQService defines the interface for business logic related to FAQs.
type FAQService interface {
	GetCategories(ctx context.Context) ([]string, error)
	GetQuestionsByCategory(ctx context.Context, category string) ([]*domain.FAQ, error)
	GetAnswer(ctx context.Context, faqID primitive.ObjectID) (*domain.FAQ, error)
}

// InstructionService defines the interface for business logic related to instructions.
type InstructionService interface {
	GetPlatforms(ctx context.Context) ([]string, error)
	GetInstructionsByPlatform(ctx context.Context, platform string) ([]*domain.Instruction, error)
	GetInstructionDetails(ctx context.Context, instructionID primitive.ObjectID) (*domain.Instruction, error)
}

// SubscriptionService предоставляет операции для управления подписками
type SubscriptionService interface {
	CreateSubscription(ctx context.Context, userID primitive.ObjectID, planID primitive.ObjectID) (*domain.Subscription, error)
	GetActiveSubscriptionForUser(ctx context.Context, userID primitive.ObjectID) ([]*domain.SubscriptionDetails, error)
	GetUserActiveSubscription(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error)
	GetSubscriptionByID(ctx context.Context, subID primitive.ObjectID) (*domain.Subscription, error)
	ConfigureSubscriptionServer(ctx context.Context, userID primitive.ObjectID, subID primitive.ObjectID, serverID primitive.ObjectID) error
	GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error)
	FindAndExpireSubscriptions(ctx context.Context) error
	GetSubscriptionQRCode(ctx context.Context, sub *domain.Subscription) ([]byte, error)
	ActivateSubscription(ctx context.Context, userID, planID, paymentID primitive.ObjectID) error
}

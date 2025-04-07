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

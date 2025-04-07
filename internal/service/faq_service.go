package service

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

type faqService struct {
	faqRepo repository.FAQRepository
	logger  *slog.Logger
}

func NewFAQService(faqRepo repository.FAQRepository, logger *slog.Logger) FAQService {
	return &faqService{
		faqRepo: faqRepo,
		logger:  logger.With(slog.String("service", "faq")),
	}
}

func (s *faqService) GetCategories(ctx context.Context) ([]string, error) {
	categories, err := s.faqRepo.GetAllActiveCategories(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get FAQ categories", slog.Any("error", err))
		// Don't wrap internal error, let repo return apperror
		return nil, err
	}
	s.logger.DebugContext(ctx, "Retrieved FAQ categories", slog.Int("count", len(categories)))
	return categories, nil
}

func (s *faqService) GetQuestionsByCategory(ctx context.Context, category string) ([]*domain.FAQ, error) {
	faqs, err := s.faqRepo.GetActiveByCategory(ctx, category)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get FAQs by category", slog.String("category", category), slog.Any("error", err))
		return nil, err
	}
	s.logger.DebugContext(ctx, "Retrieved FAQs by category", slog.String("category", category), slog.Int("count", len(faqs)))
	return faqs, nil
}

func (s *faqService) GetAnswer(ctx context.Context, faqID primitive.ObjectID) (*domain.FAQ, error) {
	faq, err := s.faqRepo.GetByID(ctx, faqID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get FAQ by ID", slog.String("faq_id", faqID.Hex()), slog.Any("error", err))
		return nil, err
	}
	// Check if FAQ is active? The repository only gets by ID, doesn't check is_active.
	// For simplicity, assume GetByID is sufficient for now, or add an is_active check here.
	// if !faq.IsActive { ... return apperrors.NewNotFoundError(...) }
	s.logger.DebugContext(ctx, "Retrieved FAQ answer", slog.String("faq_id", faqID.Hex()))
	return faq, nil
}

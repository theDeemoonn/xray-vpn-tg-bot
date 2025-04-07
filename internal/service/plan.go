package service

import (
	"context"
	"errors"
	"log/slog"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PlanService defines the interface for plan-related operations needed by the bot/handlers.
type PlanService interface {
	GetActivePlans(ctx context.Context) ([]*domain.Plan, error)
	GetPlanByID(ctx context.Context, id primitive.ObjectID) (*domain.Plan, error)
}

type planService struct {
	planRepo repository.PlanRepository
	logger   *slog.Logger
}

func NewPlanService(planRepo repository.PlanRepository, logger *slog.Logger) PlanService {
	return &planService{
		planRepo: planRepo,
		logger:   logger.With(slog.String("service", "plan")),
	}
}

// GetActivePlans retrieves all currently active plans, ordered by SortOrder.
func (s *planService) GetActivePlans(ctx context.Context) ([]*domain.Plan, error) {
	s.logger.DebugContext(ctx, "Getting active plans")
	plans, err := s.planRepo.GetAllActive(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get active plans from repository", slog.Any("error", err))
		return nil, err
	}
	s.logger.InfoContext(ctx, "Retrieved active plans", slog.Int("count", len(plans)))
	return plans, nil
}

// GetPlanByID retrieves a single plan by its ID.
func (s *planService) GetPlanByID(ctx context.Context, id primitive.ObjectID) (*domain.Plan, error) {
	s.logger.DebugContext(ctx, "Getting plan by ID", slog.String("plan_id", id.Hex()))
	plan, err := s.planRepo.GetByID(ctx, id)
	if err != nil {
		// Check for NotFound error code
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Plan not found by ID in repository", slog.String("plan_id", id.Hex()))
			return nil, apperrors.ErrPlanNotFound // Return predefined error
		}
		s.logger.ErrorContext(ctx, "Failed to get plan by ID from repository", slog.String("plan_id", id.Hex()), slog.Any("error", err))
		return nil, err // Return original error (likely internal)
	}
	s.logger.InfoContext(ctx, "Retrieved plan by ID", slog.String("plan_id", id.Hex()))
	return plan, nil
}

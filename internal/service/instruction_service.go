package service

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

type instructionService struct {
	instructionRepo repository.InstructionRepository
	logger          *slog.Logger
}

func NewInstructionService(instructionRepo repository.InstructionRepository, logger *slog.Logger) InstructionService {
	return &instructionService{
		instructionRepo: instructionRepo,
		logger:          logger.With(slog.String("service", "instruction")),
	}
}

func (s *instructionService) GetPlatforms(ctx context.Context) ([]string, error) {
	platforms, err := s.instructionRepo.GetAllActivePlatforms(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get instruction platforms", slog.Any("error", err))
		return nil, err
	}
	s.logger.DebugContext(ctx, "Retrieved instruction platforms", slog.Int("count", len(platforms)))
	return platforms, nil
}

func (s *instructionService) GetInstructionsByPlatform(ctx context.Context, platform string) ([]*domain.Instruction, error) {
	instructions, err := s.instructionRepo.GetActiveByPlatform(ctx, platform)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get instructions by platform", slog.String("platform", platform), slog.Any("error", err))
		return nil, err
	}
	s.logger.DebugContext(ctx, "Retrieved instructions by platform", slog.String("platform", platform), slog.Int("count", len(instructions)))
	return instructions, nil
}

func (s *instructionService) GetInstructionDetails(ctx context.Context, instructionID primitive.ObjectID) (*domain.Instruction, error) {
	instruction, err := s.instructionRepo.GetByID(ctx, instructionID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get instruction by ID", slog.String("instruction_id", instructionID.Hex()), slog.Any("error", err))
		return nil, err
	}
	// Similarly, consider an IsActive check here if needed.
	s.logger.DebugContext(ctx, "Retrieved instruction details", slog.String("instruction_id", instructionID.Hex()))
	return instruction, nil
}

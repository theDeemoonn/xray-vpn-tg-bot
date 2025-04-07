package mongodb

import (
	"context"
	"errors"
	"log/slog"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

type InstructionRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewInstructionRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.InstructionRepository {
	return &InstructionRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

func (r *InstructionRepo) GetAllActivePlatforms(ctx context.Context) ([]string, error) {
	filter := bson.M{"is_active": true}
	results, err := r.collection.Distinct(ctx, "platform", filter)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get distinct active instruction platforms", slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить платформы инструкций", err)
	}

	platforms := make([]string, 0, len(results))
	for _, result := range results {
		if platform, ok := result.(string); ok {
			platforms = append(platforms, platform)
		} else {
			r.logger.WarnContext(ctx, "Non-string platform found in distinct results", slog.Any("value", result))
		}
	}
	// Consider sorting platforms here if needed
	r.logger.DebugContext(ctx, "Retrieved active instruction platforms", slog.Int("count", len(platforms)))
	return platforms, nil
}

func (r *InstructionRepo) GetActiveByPlatform(ctx context.Context, platform string) ([]*domain.Instruction, error) {
	filter := bson.M{"platform": platform, "is_active": true}
	findOptions := options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to query active instructions by platform", slog.String("platform", platform), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить инструкции по платформе", err)
	}
	defer cursor.Close(ctx)

	var instructions []*domain.Instruction
	if err := cursor.All(ctx, &instructions); err != nil {
		r.logger.ErrorContext(ctx, "Failed to decode active instructions by platform", slog.String("platform", platform), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось декодировать инструкции", err)
	}

	if instructions == nil {
		instructions = []*domain.Instruction{} // Return empty slice instead of nil
	}

	r.logger.DebugContext(ctx, "Retrieved active instructions by platform", slog.String("platform", platform), slog.Int("count", len(instructions)))
	return instructions, nil
}

func (r *InstructionRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Instruction, error) {
	var instruction domain.Instruction
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&instruction)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("Инструкция", id.Hex(), err)
		}
		r.logger.ErrorContext(ctx, "Failed to find instruction by id", slog.String("id", id.Hex()), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось найти инструкцию по ID", err)
	}
	return &instruction, nil
}

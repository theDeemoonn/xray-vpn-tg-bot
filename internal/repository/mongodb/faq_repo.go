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

type FAQRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewFAQRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.FAQRepository {
	return &FAQRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

func (r *FAQRepo) GetAllActiveCategories(ctx context.Context) ([]string, error) {
	filter := bson.M{"is_active": true}
	results, err := r.collection.Distinct(ctx, "category", filter)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get distinct active FAQ categories", slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить категории FAQ", err)
	}

	categories := make([]string, 0, len(results))
	for _, result := range results {
		if category, ok := result.(string); ok {
			categories = append(categories, category)
		} else {
			r.logger.WarnContext(ctx, "Non-string category found in distinct results", slog.Any("value", result))
		}
	}
	// Consider sorting categories alphabetically here if needed
	// sort.Strings(categories)
	r.logger.DebugContext(ctx, "Retrieved active FAQ categories", slog.Int("count", len(categories)))
	return categories, nil
}

func (r *FAQRepo) GetActiveByCategory(ctx context.Context, category string) ([]*domain.FAQ, error) {
	filter := bson.M{"category": category, "is_active": true}
	findOptions := options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to query active FAQs by category", slog.String("category", category), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить FAQ по категории", err)
	}
	defer cursor.Close(ctx)

	var faqs []*domain.FAQ
	if err := cursor.All(ctx, &faqs); err != nil {
		r.logger.ErrorContext(ctx, "Failed to decode active FAQs by category", slog.String("category", category), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось декодировать FAQ", err)
	}

	if faqs == nil {
		faqs = []*domain.FAQ{} // Return empty slice instead of nil
	}

	r.logger.DebugContext(ctx, "Retrieved active FAQs by category", slog.String("category", category), slog.Int("count", len(faqs)))
	return faqs, nil
}

func (r *FAQRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.FAQ, error) {
	var faq domain.FAQ
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&faq)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("FAQ", id.Hex(), err)
		}
		r.logger.ErrorContext(ctx, "Failed to find FAQ by id", slog.String("id", id.Hex()), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось найти FAQ по ID", err)
	}
	return &faq, nil
}

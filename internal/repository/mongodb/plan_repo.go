package mongodb

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

type PlanRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewPlanRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.PlanRepository {
	return &PlanRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

func (r *PlanRepo) Create(ctx context.Context, plan *domain.Plan) error {
	if plan.ID.IsZero() {
		plan.ID = primitive.NewObjectID()
	}
	now := time.Now()
	plan.CreatedAt = now
	plan.UpdatedAt = now

	count, err := r.collection.CountDocuments(ctx, bson.M{"name": plan.Name})
	if err != nil {
		r.logger.Error("Failed to count documents for create check", slog.String("name", plan.Name), slog.Any("error", err))
		return apperrors.NewInternalError("Не удалось проверить имя плана", err)
	}
	if count > 0 {
		r.logger.Warn("Attempted to create plan with existing name", slog.String("name", plan.Name))
		return apperrors.NewConflictError("План с таким именем уже существует", nil)
	}

	_, err = r.collection.InsertOne(ctx, plan)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			r.logger.Warn("Attempted to create plan with duplicate key (likely name)", slog.String("name", plan.Name))
			return apperrors.NewConflictError("План с таким именем уже существует (ошибка индекса)", err)
		}
		r.logger.Error("Failed to insert plan", slog.String("error", err.Error()), slog.String("name", plan.Name))
		return apperrors.NewInternalError("Не удалось создать план", err)
	}
	r.logger.Debug("Plan created successfully", slog.String("plan_id", plan.ID.Hex()))
	return nil
}

func (r *PlanRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Plan, error) {
	var plan domain.Plan
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&plan)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("План", id.Hex(), err)
		}
		r.logger.Error("Failed to find plan by id", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти план по ID", err)
	}
	return &plan, nil
}

func (r *PlanRepo) GetAllActive(ctx context.Context) ([]*domain.Plan, error) {
	filter := bson.M{"is_active": true}
	findOptions := options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}, {Key: "price", Value: 1}})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.Error("Failed to query active plans", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось получить активные планы", err)
	}
	defer cursor.Close(ctx)

	var plans []*domain.Plan
	if err := cursor.All(ctx, &plans); err != nil {
		r.logger.Error("Failed to decode active plans", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать активные планы", err)
	}

	if plans == nil {
		plans = []*domain.Plan{} // Return empty slice instead of nil
	}

	r.logger.Debug("Retrieved active plans", slog.Int("count", len(plans)))
	return plans, nil
}

func (r *PlanRepo) Update(ctx context.Context, plan *domain.Plan) error {
	plan.UpdatedAt = time.Now()
	filter := bson.M{"_id": plan.ID}

	count, err := r.collection.CountDocuments(ctx, bson.M{"name": plan.Name, "_id": bson.M{"$ne": plan.ID}})
	if err != nil {
		r.logger.Error("Failed to count documents for update check", slog.String("id", plan.ID.Hex()), slog.String("name", plan.Name), slog.Any("error", err))
		return apperrors.NewInternalError("Не удалось проверить имя плана перед обновлением", err)
	}
	if count > 0 {
		r.logger.Warn("Attempted to update plan resulting in duplicate name", slog.String("id", plan.ID.Hex()), slog.String("name", plan.Name))
		return apperrors.NewConflictError("План с таким именем уже существует", nil)
	}

	updateSet := bson.M{
		"name":        plan.Name,
		"description": plan.Description,
		"price":       plan.Price,
		"currency":    plan.Currency,
		"duration":    plan.Duration,
		"traffic_gb":  plan.TrafficGB,
		"is_active":   plan.IsActive,
		"sort_order":  plan.SortOrder,
		"updated_at":  plan.UpdatedAt,
	}
	update := bson.M{"$set": updateSet}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			r.logger.Warn("Attempted to update plan with duplicate key (likely name)", slog.String("id", plan.ID.Hex()), slog.String("name", plan.Name))
			return apperrors.NewConflictError("План с таким именем уже существует (ошибка индекса)", err)
		}
		r.logger.Error("Failed to update plan", slog.String("id", plan.ID.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось обновить план", err)
	}

	if result.MatchedCount == 0 {
		r.logger.Warn("Update plan failed: plan not found", slog.String("id", plan.ID.Hex()))
		return apperrors.NewNotFoundError("План", plan.ID.Hex(), nil)
	}

	r.logger.Debug("Plan updated successfully", slog.String("id", plan.ID.Hex()), slog.Int64("modified_count", result.ModifiedCount))
	return nil
}

func (r *PlanRepo) Delete(ctx context.Context, id primitive.ObjectID) error {
	filter := bson.M{"_id": id}
	result, err := r.collection.DeleteOne(ctx, filter)
	if err != nil {
		r.logger.Error("Failed to delete plan", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось удалить план", err)
	}
	if result.DeletedCount == 0 {
		r.logger.Warn("Delete plan failed: plan not found", slog.String("id", id.Hex()))
		return apperrors.NewNotFoundError("План", id.Hex(), nil)
	}
	r.logger.Info("Plan deleted successfully", slog.String("id", id.Hex()))
	return nil
}

// EnsureIndexes creates necessary indexes for the plan collection.
func (r *PlanRepo) EnsureIndexes(ctx context.Context) error {
	r.logger.Info("Ensuring indexes for plan collection...")
	indexModels := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "is_active", Value: 1}, {Key: "sort_order", Value: 1}, {Key: "price", Value: 1}},
			Options: options.Index().SetName("active_sorted"),
		},
		{
			Keys:    bson.D{{Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("name_unique"),
		},
	}

	indexView := r.collection.Indexes()
	_, err := indexView.CreateMany(ctx, indexModels)
	if err != nil {
		var cmdErr *mongo.CommandError
		if errors.As(err, &cmdErr) && (cmdErr.Code == 85 || cmdErr.Code == 86) {
			r.logger.Warn("Plan index already exists or conflicts, skipping creation.", slog.Any("error", err))
			return nil // Not fatal for EnsureIndexes
		}
		r.logger.Error("Failed to create plan indexes", slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось создать индексы для планов", err)
	}
	r.logger.Info("Plan indexes ensured successfully")
	return nil
}

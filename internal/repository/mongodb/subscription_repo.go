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

	"xray-vpn-tg-bot/internal/apperrors" // Import apperrors
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

type SubscriptionRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewSubscriptionRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.SubscriptionRepository {
	return &SubscriptionRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

func (r *SubscriptionRepo) Create(ctx context.Context, sub *domain.Subscription) error {
	if sub.ID.IsZero() {
		sub.ID = primitive.NewObjectID()
	}
	now := time.Now()
	sub.CreatedAt = now
	sub.UpdatedAt = now

	_, err := r.collection.InsertOne(ctx, sub)
	if err != nil {
		// Handle potential duplicate key errors if unique indexes are added (e.g., user_id + status=active)
		if mongo.IsDuplicateKeyError(err) {
			// This might indicate a logic error - trying to create a duplicate active sub for a user
			r.logger.Error("Attempted to create subscription with duplicate key", slog.String("user_id", sub.UserID.Hex()), slog.String("plan_id", sub.PlanID.Hex()), slog.String("status", string(sub.Status)), slog.Any("error", err))
			return apperrors.NewConflictError("Ошибка создания подписки: дублирующаяся запись", err)
		}
		r.logger.Error("Failed to insert subscription", slog.String("error", err.Error()), slog.String("user_id", sub.UserID.Hex()))
		return apperrors.NewInternalError("Не удалось создать подписку", err)
	}
	r.logger.Debug("Subscription created successfully", slog.String("sub_id", sub.ID.Hex()))
	return nil
}

func (r *SubscriptionRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&sub)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("Подписка", id.Hex(), err)
		}
		r.logger.Error("Failed to find subscription by id", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти подписку по ID", err)
	}
	return &sub, nil
}

// GetActiveByUserID finds the currently active subscription for a user.
// Assumes only one subscription can be active at a time for a user.
func (r *SubscriptionRepo) GetActiveByUserID(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error) {
	filter := bson.M{
		"user_id": userID,
		"status":  domain.SubscriptionStatusActive,
	}
	var sub domain.Subscription
	err := r.collection.FindOne(ctx, filter).Decode(&sub)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// This is not necessarily an error, just means no active subscription
			return nil, apperrors.ErrSubscriptionNotFound // Use predefined error
		}
		r.logger.Error("Failed to find active subscription by user id", slog.String("user_id", userID.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти активную подписку пользователя", err)
	}
	return &sub, nil
}

// GetActiveByUserIDAndServerID finds an active subscription for a user on a specific server.
func (r *SubscriptionRepo) GetActiveByUserIDAndServerID(ctx context.Context, userID, serverID primitive.ObjectID) (*domain.Subscription, error) {
	filter := bson.M{
		"user_id":   userID,
		"server_id": serverID,
		"status":    domain.SubscriptionStatusActive,
	}
	var sub domain.Subscription
	err := r.collection.FindOne(ctx, filter).Decode(&sub)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.ErrSubscriptionNotFound // Use predefined error
		}
		r.logger.Error("Failed to find active subscription by user and server id", slog.String("user_id", userID.Hex()), slog.String("server_id", serverID.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти активную подписку пользователя на сервере", err)
	}
	return &sub, nil
}

// ListByUserID retrieves all subscriptions for a given user, optionally sorted.
func (r *SubscriptionRepo) ListByUserID(ctx context.Context, userID primitive.ObjectID) ([]*domain.Subscription, error) {
	filter := bson.M{"user_id": userID}
	findOptions := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}) // Sort by most recent first

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.Error("Failed to query subscriptions by user id", slog.String("user_id", userID.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось получить подписки пользователя", err)
	}
	defer cursor.Close(ctx)

	var subs []*domain.Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		r.logger.Error("Failed to decode subscriptions for user", slog.String("user_id", userID.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать подписки пользователя", err)
	}

	if subs == nil {
		subs = []*domain.Subscription{}
	}

	r.logger.Debug("Retrieved subscriptions for user", slog.String("user_id", userID.Hex()), slog.Int("count", len(subs)))
	return subs, nil
}

func (r *SubscriptionRepo) Update(ctx context.Context, sub *domain.Subscription) error {
	sub.UpdatedAt = time.Now()
	filter := bson.M{"_id": sub.ID}

	// Explicitly set fields to update
	updateSet := bson.M{
		"plan_id":        sub.PlanID,
		"server_id":      sub.ServerID,
		"user_id":        sub.UserID,
		"xui_inbound_id": sub.XuiInboundID, // Corrected field name
		"xui_client_uid": sub.XuiClientUID, // Corrected field name
		"status":         sub.Status,
		"activated_at":   sub.ActivatedAt, // Corrected field name
		"expires_at":     sub.ExpiresAt,
		"traffic_limit":  sub.TrafficLimit,
		"traffic_used":   sub.TrafficUsed,
		"auto_renew":     sub.AutoRenew,
		"payment_id":     sub.PaymentID,
		"config_link":    sub.ConfigLink,
		"updated_at":     sub.UpdatedAt,
	}
	update := bson.M{"$set": updateSet}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		r.logger.Error("Failed to update subscription", slog.String("sub_id", sub.ID.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось обновить подписку", err)
	}

	if result.MatchedCount == 0 {
		r.logger.Warn("Update subscription failed: subscription not found", slog.String("sub_id", sub.ID.Hex()))
		return apperrors.ErrSubscriptionNotFound // Use predefined error
	}

	r.logger.Debug("Subscription updated successfully", slog.String("sub_id", sub.ID.Hex()), slog.Int64("modified_count", result.ModifiedCount))
	return nil
}

// FindToExpire finds active subscriptions that will expire within the given duration.
func (r *SubscriptionRepo) FindToExpire(ctx context.Context, within time.Duration) ([]*domain.Subscription, error) {
	now := time.Now()
	expiryThreshold := now.Add(within)
	filter := bson.M{
		"status": domain.SubscriptionStatusActive,
		"expires_at": bson.M{
			"$gt":  now, // Already expired shouldn't be notified
			"$lte": expiryThreshold,
		},
		// Optionally exclude those with auto-renew?
		// "auto_renew": false,
	}

	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		r.logger.Error("Failed to query subscriptions expiring soon", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти подписки, истекающие скоро", err)
	}
	defer cursor.Close(ctx)

	var subs []*domain.Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		r.logger.Error("Failed to decode subscriptions expiring soon", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать подписки, истекающие скоро", err)
	}

	if subs == nil {
		subs = []*domain.Subscription{}
	}
	r.logger.Debug("Found subscriptions expiring soon", slog.Int("count", len(subs)), slog.Duration("within", within))
	return subs, nil
}

// FindActiveNeedingRenewal finds active subscriptions with auto-renewal enabled that have expired.
func (r *SubscriptionRepo) FindActiveNeedingRenewal(ctx context.Context) ([]*domain.Subscription, error) {
	now := time.Now()
	filter := bson.M{
		"status":     domain.SubscriptionStatusActive, // Or should we look for expired ones?
		"auto_renew": true,
		"expires_at": bson.M{"lte": now}, // Expired or expiring right now
	}

	cursor, err := r.collection.Find(ctx, filter)
	if err != nil {
		r.logger.Error("Failed to query subscriptions needing renewal", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти подписки для продления", err)
	}
	defer cursor.Close(ctx)

	var subs []*domain.Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		r.logger.Error("Failed to decode subscriptions needing renewal", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать подписки для продления", err)
	}

	if subs == nil {
		subs = []*domain.Subscription{}
	}
	r.logger.Debug("Found subscriptions needing renewal", slog.Int("count", len(subs)))
	return subs, nil
}

// EnsureIndexes creates necessary indexes for the subscription collection.
func (r *SubscriptionRepo) EnsureIndexes(ctx context.Context) error {
	r.logger.Info("Ensuring indexes for subscription collection...")
	indexModels := []mongo.IndexModel{
		{
			// Index for finding active subscriptions by user
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("user_status"),
		},
		{
			// Index for finding active subscriptions by user and server
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "server_id", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("user_server_status"),
		},
		{
			// Index for finding expiring subscriptions
			Keys:    bson.D{{Key: "status", Value: 1}, {Key: "expires_at", Value: 1}},
			Options: options.Index().SetName("status_expires"),
		},
		{
			// Index for finding subscriptions needing renewal
			Keys:    bson.D{{Key: "status", Value: 1}, {Key: "auto_renew", Value: 1}, {Key: "expires_at", Value: 1}},
			Options: options.Index().SetName("status_autorenew_expires"),
		},
		{
			// Index on xui_client_uid for potential lookups (if needed by other services)
			Keys:    bson.D{{Key: "xui_client_uid", Value: 1}, {Key: "server_id", Value: 1}},
			Options: options.Index().SetName("xui_client_server").SetSparse(true), // Sparse if not all subs have it
		},
	}

	indexView := r.collection.Indexes()
	_, err := indexView.CreateMany(ctx, indexModels)
	if err != nil {
		var cmdErr *mongo.CommandError
		if errors.As(err, &cmdErr) && (cmdErr.Code == 85 || cmdErr.Code == 86) {
			r.logger.Warn("Subscription index already exists or conflicts, skipping creation.", slog.Any("error", err))
			return nil
		}
		r.logger.Error("Failed to create subscription indexes", slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось создать индексы для подписок", err)
	}
	r.logger.Info("Subscription indexes ensured successfully")
	return nil
}

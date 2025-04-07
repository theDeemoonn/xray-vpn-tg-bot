package mongodb

import (
	"context"
	"errors"
	"fmt"
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

type PaymentRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewPaymentRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.PaymentRepository {
	return &PaymentRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

func (r *PaymentRepo) Create(ctx context.Context, payment *domain.Payment) error {
	if payment.ID.IsZero() {
		payment.ID = primitive.NewObjectID()
	}
	now := time.Now()
	payment.CreatedAt = now
	payment.UpdatedAt = now

	_, err := r.collection.InsertOne(ctx, payment)
	if err != nil {
		// Handle potential duplicate provider_payment_id if indexed unique
		if mongo.IsDuplicateKeyError(err) {
			errMsg := fmt.Sprintf("Платеж с ID провайдера '%s' (%s) уже существует", payment.ProviderPaymentID, payment.Provider)
			r.logger.Warn(errMsg, slog.String("provider", string(payment.Provider)), slog.String("provider_id", payment.ProviderPaymentID))
			return apperrors.NewConflictError(errMsg, err)
		}
		r.logger.Error("Failed to insert payment", slog.String("error", err.Error()), slog.String("user_id", payment.UserID.Hex()))
		return apperrors.NewInternalError("Не удалось создать платеж", err)
	}
	r.logger.Debug("Payment created successfully", slog.String("payment_id", payment.ID.Hex()))
	return nil
}

func (r *PaymentRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Payment, error) {
	var payment domain.Payment
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&payment)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("Платеж", id.Hex(), err)
		}
		r.logger.Error("Failed to find payment by id", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти платеж по ID", err)
	}
	return &payment, nil
}

func (r *PaymentRepo) GetByProviderPaymentID(ctx context.Context, provider domain.PaymentProvider, providerID string) (*domain.Payment, error) {
	filter := bson.M{
		"provider":            provider,
		"provider_payment_id": providerID,
	}
	var payment domain.Payment
	err := r.collection.FindOne(ctx, filter).Decode(&payment)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("Платеж", fmt.Sprintf("%s:%s", provider, providerID), err)
		}
		r.logger.Error("Failed to find payment by provider id", slog.String("provider", string(provider)), slog.String("provider_id", providerID), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти платеж по ID провайдера", err)
	}
	return &payment, nil
}

func (r *PaymentRepo) Update(ctx context.Context, payment *domain.Payment) error {
	payment.UpdatedAt = time.Now()
	filter := bson.M{"_id": payment.ID}

	// Explicitly set fields to update
	updateSet := bson.M{
		"user_id":             payment.UserID,
		"plan_id":             payment.PlanID,
		"subscription_id":     payment.SubscriptionID,
		"provider":            payment.Provider,
		"provider_payment_id": payment.ProviderPaymentID,
		"amount":              payment.Amount,
		"currency":            payment.Currency,
		"status":              payment.Status,
		// PaidAt is not a field in domain.Payment
		"metadata":          payment.Metadata,
		"is_gift":           payment.IsGift,
		"gift_recipient_id": payment.GiftRecipientID,
		"updated_at":        payment.UpdatedAt,
	}
	update := bson.M{"$set": updateSet}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		r.logger.Error("Failed to update payment", slog.String("payment_id", payment.ID.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось обновить платеж", err)
	}

	if result.MatchedCount == 0 {
		r.logger.Warn("Update payment failed: payment not found", slog.String("payment_id", payment.ID.Hex()))
		return apperrors.NewNotFoundError("Платеж", payment.ID.Hex(), nil)
	}

	r.logger.Debug("Payment updated successfully", slog.String("payment_id", payment.ID.Hex()), slog.Int64("modified_count", result.ModifiedCount))
	return nil
}

// EnsureIndexes creates necessary indexes for the payment collection.
func (r *PaymentRepo) EnsureIndexes(ctx context.Context) error {
	r.logger.Info("Ensuring indexes for payment collection...")
	indexModels := []mongo.IndexModel{
		{
			// Index for finding payments by provider ID
			Keys:    bson.D{{Key: "provider", Value: 1}, {Key: "provider_payment_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("provider_id_unique"),
		},
		{
			// Index for finding payments by user
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("user_created"),
		},
		{
			// Index for finding pending payments
			Keys:    bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: 1}},
			Options: options.Index().SetName("status_created").SetPartialFilterExpression(bson.M{"status": domain.PaymentStatusPending}),
		},
	}

	indexView := r.collection.Indexes()
	_, err := indexView.CreateMany(ctx, indexModels)
	if err != nil {
		var cmdErr *mongo.CommandError
		if errors.As(err, &cmdErr) && (cmdErr.Code == 85 || cmdErr.Code == 86) {
			r.logger.Warn("Payment index already exists or conflicts, skipping creation.", slog.Any("error", err))
			return nil
		}
		r.logger.Error("Failed to create payment indexes", slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось создать индексы для платежей", err)
	}
	r.logger.Info("Payment indexes ensured successfully")
	return nil
}

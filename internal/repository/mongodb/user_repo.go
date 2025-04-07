package mongodb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type UserRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewUserRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.UserRepository {
	return &UserRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

// Create inserts a new user into the database.
func (r *UserRepo) Create(ctx context.Context, user *domain.User) error {
	// Ensure ID is generated if it's zero
	if user.ID.IsZero() {
		user.ID = primitive.NewObjectID()
	}
	now := time.Now()
	user.CreatedAt = now
	user.UpdatedAt = now

	_, err := r.collection.InsertOne(ctx, user)
	if err != nil {
		// Handle potential duplicate key error (e.g., if telegram_id is unique index)
		if mongo.IsDuplicateKeyError(err) {
			r.logger.Warn("Attempted to create user with duplicate telegram_id", slog.Int64("telegram_id", user.TelegramID))
			// Return a specific conflict error from apperrors
			return apperrors.NewConflictError("Пользователь с таким Telegram ID уже существует", err)
		}
		r.logger.Error("Failed to insert user", slog.String("error", err.Error()), slog.Int64("telegram_id", user.TelegramID))
		// Wrap the unexpected database error
		return apperrors.NewInternalError("Не удалось создать пользователя", err)
	}
	r.logger.Debug("User created successfully", slog.String("user_id", user.ID.Hex()))
	return nil
}

// GetByID retrieves a user by their MongoDB ObjectID.
func (r *UserRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.User, error) {
	var user domain.User
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Use updated NewNotFoundError signature
			return nil, apperrors.NewNotFoundError("Пользователь", id.Hex(), err)
		}
		r.logger.Error("Failed to find user by id", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти пользователя по ID", err)
	}
	return &user, nil
}

// GetByTelegramID retrieves a user by their Telegram ID.
func (r *UserRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	var user domain.User
	filter := bson.M{"telegram_id": telegramID}
	err := r.collection.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Use updated NewNotFoundError signature
			return nil, apperrors.NewNotFoundError("Пользователь", fmt.Sprintf("telegram_id=%d", telegramID), err)
		}
		r.logger.Error("Failed to find user by telegram id", slog.Int64("telegram_id", telegramID), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти пользователя по Telegram ID", err)
	}
	return &user, nil
}

// Update updates an existing user in the database.
func (r *UserRepo) Update(ctx context.Context, user *domain.User) error {
	user.UpdatedAt = time.Now()

	filter := bson.M{"_id": user.ID}
	// Use bson.M for $set to avoid replacing the whole document
	// Only update fields that are typically changed
	updateSet := bson.M{
		"username":        user.Username,
		"first_name":      user.FirstName,
		"last_name":       user.LastName,
		"language_code":   user.LanguageCode,
		"is_premium":      user.IsPremium,
		"balance":         user.Balance,
		"referral_code":   user.ReferralCode, // Can this be updated?
		"referred_by":     user.ReferredBy,
		"agreed_to_terms": user.AgreedToTerms,
		"updated_at":      user.UpdatedAt,
	}
	update := bson.M{"$set": updateSet}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		r.logger.Error("Failed to update user", slog.String("id", user.ID.Hex()), slog.String("error", err.Error()))
		// Wrap the unexpected database error
		return apperrors.NewInternalError("Не удалось обновить пользователя", err)
	}

	if result.MatchedCount == 0 {
		r.logger.Warn("Update user failed: user not found", slog.String("id", user.ID.Hex()))
		// Use updated NewNotFoundError signature
		return apperrors.NewNotFoundError("Пользователь", user.ID.Hex(), nil)
	}

	r.logger.Debug("User updated successfully", slog.String("id", user.ID.Hex()), slog.Int64("modified_count", result.ModifiedCount))
	return nil
}

// --- Indexes ---

// EnsureIndexes creates necessary indexes for the user collection.
func (r *UserRepo) EnsureIndexes(ctx context.Context) error {
	r.logger.Info("Ensuring indexes for user collection...")
	indexModels := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "telegram_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("telegram_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "referral_code", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("referral_code_unique_sparse"),
		},
		{
			Keys:    bson.D{{Key: "created_at", Value: -1}},
			Options: options.Index().SetName("created_at_desc"),
		},
	}

	indexView := r.collection.Indexes()
	_, err := indexView.CreateMany(ctx, indexModels)
	if err != nil {
		// Ignore "IndexOptionsConflict" and "IndexKeySpecsConflict" errors,
		// as they mean the index already exists (potentially with different options).
		// For production, you might want more robust index management.
		var cmdErr *mongo.CommandError
		if errors.As(err, &cmdErr) && (cmdErr.Code == 85 || cmdErr.Code == 86) {
			r.logger.Warn("Index already exists or conflicts, skipping creation.", slog.Any("error", err))
			return nil // Not a fatal error for EnsureIndexes
		}
		r.logger.Error("Failed to create user indexes", slog.String("error", err.Error()))
		// Wrap the unexpected database error
		return apperrors.NewInternalError("Не удалось создать индексы для пользователей", err)
	}
	r.logger.Info("User indexes ensured successfully")
	return nil
}

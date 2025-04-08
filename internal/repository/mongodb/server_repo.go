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

type ServerRepo struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

func NewServerRepo(db *mongo.Database, collectionName string, logger *slog.Logger) repository.ServerRepository {
	return &ServerRepo{
		collection: db.Collection(collectionName),
		logger:     logger,
	}
}

// Create inserts a new server into the database.
func (r *ServerRepo) Create(ctx context.Context, server *domain.Server) error {
	if server.ID.IsZero() {
		server.ID = primitive.NewObjectID()
	}
	now := time.Now()
	server.CreatedAt = now
	server.UpdatedAt = now

	_, err := r.collection.InsertOne(ctx, server)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			// Determine if it's duplicate name or api_url based on index name if available
			errMsg := "Сервер с таким именем или API URL уже существует"
			r.logger.Warn(errMsg, slog.String("name", server.Name), slog.String("api_url", server.ApiHost))
			return apperrors.NewConflictError(errMsg, err)
		}
		r.logger.Error("Failed to insert server", slog.String("error", err.Error()), slog.String("name", server.Name))
		return apperrors.NewInternalError("Не удалось создать сервер", err)
	}
	r.logger.Debug("Server created successfully", slog.String("server_id", server.ID.Hex()))
	return nil
}

// GetByID retrieves a server by its MongoDB ObjectID.
func (r *ServerRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.Server, error) {
	var server domain.Server
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&server)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, apperrors.NewNotFoundError("Сервер", id.Hex(), err)
		}
		r.logger.Error("Failed to find server by id", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось найти сервер по ID", err)
	}
	return &server, nil
}

// GetAllEnabled retrieves all servers that are currently enabled.
func (r *ServerRepo) GetAllEnabled(ctx context.Context) ([]*domain.Server, error) {
	filter := bson.M{"is_enabled": true}
	// Add sorting if needed, e.g., by location or name
	findOptions := options.Find().SetSort(bson.D{{Key: "location", Value: 1}, {Key: "name", Value: 1}})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.Error("Failed to query enabled servers", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось получить включенные серверы", err)
	}
	defer cursor.Close(ctx)

	var servers []*domain.Server
	if err := cursor.All(ctx, &servers); err != nil {
		r.logger.Error("Failed to decode enabled servers", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать включенные серверы", err)
	}

	if servers == nil {
		servers = []*domain.Server{} // Return empty slice instead of nil
	}

	r.logger.Debug("Retrieved enabled servers", slog.Int("count", len(servers)))
	return servers, nil
}

// GetAll retrieves all servers regardless of their enabled status.
func (r *ServerRepo) GetAll(ctx context.Context) ([]*domain.Server, error) {
	filter := bson.M{} // No filter
	findOptions := options.Find().SetSort(bson.D{{Key: "location", Value: 1}, {Key: "name", Value: 1}})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		r.logger.Error("Failed to query all servers", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось получить все серверы", err)
	}
	defer cursor.Close(ctx)

	var servers []*domain.Server
	if err := cursor.All(ctx, &servers); err != nil {
		r.logger.Error("Failed to decode all servers", slog.String("error", err.Error()))
		return nil, apperrors.NewInternalError("Не удалось декодировать все серверы", err)
	}

	if servers == nil {
		servers = []*domain.Server{} // Return empty slice instead of nil
	}

	r.logger.Debug("Retrieved all servers", slog.Int("count", len(servers)))
	return servers, nil
}

// Update updates an existing server in the database.
func (r *ServerRepo) Update(ctx context.Context, server *domain.Server) error {
	server.UpdatedAt = time.Now()

	filter := bson.M{"_id": server.ID}
	// Explicitly set fields to update to avoid overwriting CreatedAt etc.
	updateSet := bson.M{
		"name":              server.Name,
		"location":          server.Location,
		"api_host":          server.ApiHost,
		"public_host":       server.PublicHost,
		"api_username":      server.ApiUsername,
		"api_password":      server.ApiPassword,
		"target_inbound_id": server.TargetInboundID,
		"is_enabled":        server.IsEnabled,
		"is_healthy":        server.IsHealthy,
		"status":            server.Status,
		"last_check":        server.LastCheck,
		"updated_at":        server.UpdatedAt,
	}
	update := bson.M{"$set": updateSet}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			errMsg := "Сервер с таким именем или API URL уже существует"
			r.logger.Warn(errMsg, slog.String("id", server.ID.Hex()), slog.String("name", server.Name), slog.String("api_url", server.ApiHost))
			return apperrors.NewConflictError(errMsg, err)
		}
		r.logger.Error("Failed to update server", slog.String("id", server.ID.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось обновить сервер", err)
	}

	if result.MatchedCount == 0 {
		r.logger.Warn("Update server failed: server not found", slog.String("id", server.ID.Hex()))
		return apperrors.NewNotFoundError("Сервер", server.ID.Hex(), nil)
	}

	r.logger.Debug("Server updated successfully", slog.String("id", server.ID.Hex()), slog.Int64("modified_count", result.ModifiedCount))
	return nil
}

// Delete removes a server from the database.
func (r *ServerRepo) Delete(ctx context.Context, id primitive.ObjectID) error {
	filter := bson.M{"_id": id}
	result, err := r.collection.DeleteOne(ctx, filter)
	if err != nil {
		r.logger.Error("Failed to delete server", slog.String("id", id.Hex()), slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось удалить сервер", err)
	}

	if result.DeletedCount == 0 {
		r.logger.Warn("Delete server failed: server not found", slog.String("id", id.Hex()))
		return apperrors.NewNotFoundError("Сервер", id.Hex(), nil)
	}

	r.logger.Info("Server deleted successfully", slog.String("id", id.Hex()))
	return nil
}

// EnsureIndexes creates necessary indexes for the server collection.
func (r *ServerRepo) EnsureIndexes(ctx context.Context) error {
	r.logger.Info("Ensuring indexes for server collection...")
	indexModels := []mongo.IndexModel{
		{
			// Unique index on Name
			Keys:    bson.D{{Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("name_unique"),
		},
		{
			// Unique index on ApiURL
			Keys:    bson.D{{Key: "api_host", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("api_host_unique"),
		},
		{
			// Index on IsEnabled for faster filtering
			Keys:    bson.D{{Key: "is_enabled", Value: 1}},
			Options: options.Index().SetName("is_enabled"),
		},
	}

	indexView := r.collection.Indexes()
	_, err := indexView.CreateMany(ctx, indexModels)
	if err != nil {
		var cmdErr *mongo.CommandError
		if errors.As(err, &cmdErr) && (cmdErr.Code == 85 || cmdErr.Code == 86) {
			r.logger.Warn("Server index already exists or conflicts, skipping creation.", slog.Any("error", err))
			return nil
		}
		r.logger.Error("Failed to create server indexes", slog.String("error", err.Error()))
		return apperrors.NewInternalError("Не удалось создать индексы для серверов", err)
	}
	r.logger.Info("Server indexes ensured successfully")
	return nil
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
	"xray-vpn-tg-bot/internal/xui"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ServerService defines the interface for server-related operations.
type ServerService interface {
	AddServer(ctx context.Context, name, publicHost, apiHost, username, password, location string, inboundId int) (*domain.Server, error)
	GetServer(ctx context.Context, id string) (*domain.Server, error)
	ListEnabledServers(ctx context.Context) ([]*domain.Server, error)
	UpdateServer(ctx context.Context, id string, updatedServer *domain.Server) error
	DeleteServer(ctx context.Context, id string) error
	CheckServerHealth(ctx context.Context, server *domain.Server) error
	// GetXUIClientForServer(server *domain.Server) (*xui.Client, error) // Maybe needed later
}

type serverService struct {
	serverRepo repository.ServerRepository
	logger     *slog.Logger
	cfgXui     domain.XUIConfig // Need XUI config from main config
}

func NewServerService(repo repository.ServerRepository, logger *slog.Logger, cfgXui domain.XUIConfig) ServerService {
	return &serverService{
		serverRepo: repo,
		logger:     logger.With(slog.String("service", "server")),
		cfgXui:     cfgXui,
	}
}

// AddServer adds a new VPN server entry and verifies connection.
func (s *serverService) AddServer(ctx context.Context, name, publicHost, apiHost, username, password, location string, inboundId int) (*domain.Server, error) {
	s.logger.InfoContext(ctx, "Attempting to add new server", slog.String("name", name), slog.String("api_host", apiHost))
	server := domain.NewServer(name, apiHost, publicHost, username, password, location, inboundId)

	// Verify connection to x-ui panel before saving
	err := s.CheckServerHealth(ctx, server)
	if err != nil {
		s.logger.WarnContext(ctx, "Initial health check failed for new server, saving anyway", slog.String("name", name), slog.String("api_host", apiHost), slog.Any("error", err))
		server.IsHealthy = false // Mark as unhealthy initially
	}

	err = s.serverRepo.Create(ctx, server)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create server in repository", slog.String("name", name), slog.Any("error", err))
		return nil, err
	}

	s.logger.InfoContext(ctx, "Server added successfully", slog.String("server_id", server.ID.Hex()), slog.String("name", name))
	return server, nil
}

// GetServer retrieves a server by its ID string.
func (s *serverService) GetServer(ctx context.Context, id string) (*domain.Server, error) {
	s.logger.DebugContext(ctx, "Getting server by ID", slog.String("server_id_str", id))
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		s.logger.WarnContext(ctx, "Invalid server ID format received", slog.String("invalid_id", id), slog.Any("error", err))
		return nil, apperrors.NewValidationError(fmt.Sprintf("Неверный формат ID сервера: %s", id), err)
	}
	server, err := s.serverRepo.GetByID(ctx, objID)
	if err != nil {
		// Check for NotFound error code
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Server not found by ID in repository", slog.String("server_id", objID.Hex()))
			// Return predefined ServerNotFound error
			return nil, apperrors.ErrServerNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get server by ID from repository", slog.String("server_id", objID.Hex()), slog.Any("error", err))
		return nil, err // Return original error (likely Internal)
	}
	s.logger.InfoContext(ctx, "Retrieved server by ID", slog.String("server_id", objID.Hex()))
	return server, nil
}

// ListEnabledServers retrieves all active servers.
func (s *serverService) ListEnabledServers(ctx context.Context) ([]*domain.Server, error) {
	s.logger.DebugContext(ctx, "Listing enabled servers")
	servers, err := s.serverRepo.GetAllEnabled(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to list enabled servers from repository", slog.Any("error", err))
		return nil, err
	}
	s.logger.InfoContext(ctx, "Retrieved enabled servers", slog.Int("count", len(servers)))
	return servers, nil
}

// UpdateServer updates an existing server.
func (s *serverService) UpdateServer(ctx context.Context, id string, updatedServer *domain.Server) error {
	s.logger.InfoContext(ctx, "Attempting to update server", slog.String("server_id_str", id))
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		s.logger.WarnContext(ctx, "Invalid server ID format for update", slog.String("invalid_id", id), slog.Any("error", err))
		return apperrors.NewValidationError(fmt.Sprintf("Неверный формат ID сервера: %s", id), err)
	}
	updatedServer.ID = objID // Ensure ID is set

	err = s.CheckServerHealth(ctx, updatedServer)
	if err != nil {
		s.logger.WarnContext(ctx, "Health check failed before updating server, proceeding anyway", slog.String("server_id", objID.Hex()), slog.Any("error", err))
		updatedServer.IsHealthy = false
	}

	err = s.serverRepo.Update(ctx, updatedServer)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update server in repository", slog.String("server_id", objID.Hex()), slog.Any("error", err))
		return err
	}
	s.logger.InfoContext(ctx, "Server updated successfully", slog.String("server_id", objID.Hex()))
	return nil
}

// DeleteServer removes a server.
func (s *serverService) DeleteServer(ctx context.Context, id string) error {
	s.logger.InfoContext(ctx, "Attempting to delete server", slog.String("server_id_str", id))
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		s.logger.WarnContext(ctx, "Invalid server ID format for delete", slog.String("invalid_id", id), slog.Any("error", err))
		return apperrors.NewValidationError(fmt.Sprintf("Неверный формат ID сервера: %s", id), err)
	}
	err = s.serverRepo.Delete(ctx, objID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to delete server from repository", slog.String("server_id", objID.Hex()), slog.Any("error", err))
		return err
	}
	s.logger.InfoContext(ctx, "Server deleted successfully", slog.String("server_id", objID.Hex()))
	return nil
}

// CheckServerHealth attempts to log in to the server's x-ui panel.
func (s *serverService) CheckServerHealth(ctx context.Context, server *domain.Server) error {
	s.logger.DebugContext(ctx, "Checking server health", slog.String("server_name", server.Name), slog.String("api_host", server.ApiHost))
	xuiClient, err := xui.NewClient(server.ApiHost, server.ApiUsername, server.ApiPassword, s.cfgXui.APITimeout, s.logger)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create xui client for health check", slog.String("server_name", server.Name), slog.Any("error", err))
		return apperrors.NewInternalError(fmt.Sprintf("Не удалось создать X-UI клиент для %s", server.ApiHost), err)
	}

	if err := xuiClient.Login(ctx); err != nil {
		s.logger.WarnContext(ctx, "Server health check failed (login error)", slog.String("server_name", server.Name), slog.String("api_host", server.ApiHost), slog.Any("error", err))
		return apperrors.NewValidationError(fmt.Sprintf("Ошибка подключения к X-UI на %s", server.ApiHost), err)
	}

	s.logger.InfoContext(ctx, "Server health check successful", slog.String("server_name", server.Name), slog.String("api_host", server.ApiHost))
	return nil
}

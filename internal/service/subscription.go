package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
	"xray-vpn-tg-bot/internal/xui"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// SubscriptionService defines the interface for subscription-related operations.
type SubscriptionService interface {
	ActivateSubscription(ctx context.Context, userID, planID, paymentID primitive.ObjectID) error
	GetUserActiveSubscription(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error)
	GetSubscriptionByID(ctx context.Context, subID primitive.ObjectID) (*domain.Subscription, error)
	ConfigureSubscriptionServer(ctx context.Context, userID, subID, serverID primitive.ObjectID) error

	GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error)
	// Add methods for admin management (manual activation, etc.)

	FindAndExpireSubscriptions(ctx context.Context) error
}

type subscriptionService struct {
	subRepo          repository.SubscriptionRepository
	planRepo         repository.PlanRepository
	serverRepo       repository.ServerRepository
	userService      UserService
	xuiClientFactory func(server *domain.Server) (*xui.Client, error)
	logger           *slog.Logger
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subRepo repository.SubscriptionRepository,
	planRepo repository.PlanRepository,
	serverRepo repository.ServerRepository,
	userService UserService,
	xuiClientFactory func(server *domain.Server) (*xui.Client, error),
	logger *slog.Logger,
) SubscriptionService {
	return &subscriptionService{
		subRepo:          subRepo,
		planRepo:         planRepo,
		serverRepo:       serverRepo,
		userService:      userService,
		xuiClientFactory: xuiClientFactory,
		logger:           logger.With(slog.String("service", "subscription")),
	}
}

// ActivateSubscription creates or extends a user's subscription record in the database after successful payment.
func (s *subscriptionService) ActivateSubscription(ctx context.Context, userID, planID, paymentID primitive.ObjectID) error {
	s.logger.InfoContext(ctx, "Activating subscription DB record", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during DB activation", slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))
			return err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription DB activation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return err
	}

	existingSub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil && !errors.Is(err, apperrors.ErrSubscriptionNotFound) {
		s.logger.ErrorContext(ctx, "Failed to check for existing subscription during activation", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)

	if existingSub != nil {
		if existingSub.Status == domain.SubscriptionStatusActive && existingSub.ExpiresAt.After(now) {
			newExpiryDate = existingSub.ExpiresAt.Add(plan.Duration)
			s.logger.InfoContext(ctx, "Extending existing active subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("old_expiry", existingSub.ExpiresAt), slog.Time("new_expiry", newExpiryDate))
		} else {
			s.logger.InfoContext(ctx, "Reactivating/overwriting existing subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("new_expiry", newExpiryDate))
		}

		existingSub.PlanID = planID
		existingSub.ExpiresAt = newExpiryDate
		existingSub.Status = domain.SubscriptionStatusActive
		existingSub.ActivatedAt = now
		existingSub.UpdatedAt = now
		existingSub.PaymentID = paymentID
		existingSub.TrafficLimit = int64(plan.TrafficGB) * 1024 * 1024 * 1024
		existingSub.TrafficUsed = 0
		existingSub.ServerID = primitive.NilObjectID
		existingSub.XuiInboundID = 0
		existingSub.XuiClientUID = ""
		existingSub.ConfigLink = ""

		if err := s.subRepo.Update(ctx, existingSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to update existing subscription DB record", slog.String("sub_id", existingSub.ID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "Subscription DB record updated successfully", slog.String("sub_id", existingSub.ID.Hex()))

	} else {
		s.logger.InfoContext(ctx, "Creating new subscription DB record", slog.String("user_id", userID.Hex()), slog.Time("expiry", newExpiryDate))
		newSub := &domain.Subscription{
			ID:           primitive.NewObjectID(),
			UserID:       userID,
			PlanID:       planID,
			Status:       domain.SubscriptionStatusActive,
			ActivatedAt:  now,
			ExpiresAt:    newExpiryDate,
			AutoRenew:    false,
			PaymentID:    paymentID,
			CreatedAt:    now,
			UpdatedAt:    now,
			TrafficLimit: int64(plan.TrafficGB) * 1024 * 1024 * 1024,
			TrafficUsed:  0,
		}
		if err := s.subRepo.Create(ctx, newSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to create new subscription DB record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "New subscription DB record created successfully", slog.String("sub_id", newSub.ID.Hex()))
		existingSub = newSub
	}

	// --- Configure server and X-UI client ---
	subToConfigure := existingSub

	// Check if already configured (e.g., during renewal, we don't reconfigure)
	if subToConfigure.ServerID.IsZero() {
		s.logger.InfoContext(ctx, "Subscription needs server configuration", slog.String("sub_id", subToConfigure.ID.Hex()))
		// Find an available server (simple logic: take the first enabled one)
		servers, err := s.serverRepo.GetAllEnabled(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get enabled servers for auto-configuration", slog.Any("error", err))
			// Non-fatal? Log and maybe notify admin? Subscription is active in DB.
			// Return nil here as DB update was successful, but log the issue.
			s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but failed to auto-configure server. Manual intervention needed.", slog.String("sub_id", subToConfigure.ID.Hex()))
			return nil // Or return a specific warning error?
		}
		if len(servers) == 0 {
			s.logger.ErrorContext(ctx, "CRITICAL: No enabled servers found for auto-configuration. Subscription activated in DB without server.", slog.String("sub_id", subToConfigure.ID.Hex()))
			// Return nil, requires admin action.
			return nil
		}
		selectedServer := servers[0] // Take the first one

		s.logger.InfoContext(ctx, "Attempting to auto-configure server for subscription", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
		configErr := s.ConfigureSubscriptionServer(ctx, userID, subToConfigure.ID, selectedServer.ID)
		if configErr != nil {
			// Log the configuration error, but DB update was successful
			s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but auto-configuration failed.", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()), slog.Any("config_error", configErr))
			// Return nil because the payment was processed and DB updated.
			// Admin needs to handle the configuration failure.
			return nil
		}
		s.logger.InfoContext(ctx, "Auto-configuration successful", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
	} else {
		s.logger.InfoContext(ctx, "Subscription already has a server configured, skipping auto-configuration.", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", subToConfigure.ServerID.Hex()))
	}

	s.logger.InfoContext(ctx, "Subscription DB activation/update process completed", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))
	return nil
}

// GetUserActiveSubscription retrieves the active subscription for a user.
func (s *subscriptionService) GetUserActiveSubscription(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting active subscription for user", slog.String("user_id", userID.Hex()))
	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.DebugContext(ctx, "No active subscription found for user", slog.String("user_id", userID.Hex()))
			return nil, nil // Return nil, nil to indicate no active subscription found
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}
	return sub, nil
}

// GetSubscriptionByID retrieves a subscription by its ID.
func (s *subscriptionService) GetSubscriptionByID(ctx context.Context, subID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting subscription by ID", slog.String("sub_id", subID.Hex()))
	sub, err := s.subRepo.GetByID(ctx, subID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Subscription not found by ID", slog.String("sub_id", subID.Hex()))
			return nil, apperrors.ErrSubscriptionNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get subscription by ID from repository", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		return nil, err
	}
	return sub, nil
}

// ConfigureSubscriptionServer links a subscription to a server and creates the X-UI client.
func (s *subscriptionService) ConfigureSubscriptionServer(ctx context.Context, userID, subID, serverID primitive.ObjectID) error {
	s.logger.InfoContext(ctx, "Configuring server for subscription", slog.String("user_id", userID.Hex()), slog.String("sub_id", subID.Hex()), slog.String("server_id", serverID.Hex()))

	sub, err := s.GetSubscriptionByID(ctx, subID)
	if err != nil {
		return err
	}
	if sub.UserID != userID {
		s.logger.WarnContext(ctx, "User attempted to configure subscription they don't own", slog.String("user_id", userID.Hex()), slog.String("sub_id", subID.Hex()))
		return apperrors.ErrForbidden
	}
	if sub.Status != domain.SubscriptionStatusActive {
		s.logger.WarnContext(ctx, "Attempted to configure non-active subscription", slog.String("sub_id", subID.Hex()), slog.String("status", string(sub.Status)))
		return apperrors.ErrSubscriptionInactive
	}
	if !sub.ServerID.IsZero() {
		s.logger.WarnContext(ctx, "Attempted to re-configure server for subscription", slog.String("sub_id", subID.Hex()), slog.String("current_server_id", sub.ServerID.Hex()))
		return apperrors.ErrSubscriptionConfigured
	}

	server, err := s.serverRepo.GetByID(ctx, serverID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Selected server not found", slog.String("server_id", serverID.Hex()))
			return apperrors.ErrServerNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get server details for configuration", slog.String("server_id", serverID.Hex()), slog.Any("error", err))
		return err
	}
	if !server.IsEnabled {
		errMsg := fmt.Sprintf("Выбранный сервер '%s' недоступен", server.Name)
		s.logger.WarnContext(ctx, errMsg, slog.String("server_id", serverID.Hex()))
		return apperrors.NewValidationError(errMsg, nil)
	}

	xuiClientEmail := fmt.Sprintf("user_%s_%s", userID.Hex(), subID.Hex())
	xuiClientUUID := uuid.New().String()

	expiryTimeMillis := sub.ExpiresAt.UnixMilli()
	trafficLimitBytes := sub.TrafficLimit
	trafficGB := 0
	if trafficLimitBytes > 0 {
		trafficGB = int(trafficLimitBytes / (1024 * 1024 * 1024))
	}

	s.logger.InfoContext(ctx, "Creating X-UI client", slog.String("server_id", serverID.Hex()), slog.String("client_email", xuiClientEmail))
	xuiClient, err := s.xuiClientFactory(server)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create xui client instance for configuration", slog.String("server_id", serverID.Hex()), slog.Any("error", err))
		return apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	clientSettings := xui.ClientSettings{
		Email:      xuiClientEmail,
		UUID:       xuiClientUUID,
		TotalGB:    trafficGB,
		ExpiryTime: expiryTimeMillis,
		Enable:     true,
	}

	err = xuiClient.AddClient(ctx, server.TargetInboundID, clientSettings)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to add client to x-ui",
			slog.String("server_id", serverID.Hex()),
			slog.Int("inbound_id", server.TargetInboundID),
			slog.String("client_email", xuiClientEmail),
			slog.Any("error", err),
		)
		return apperrors.NewInternalError(fmt.Sprintf("Ошибка создания клиента на сервере X-UI %s", server.Name), err)
	}
	s.logger.InfoContext(ctx, "X-UI client created successfully", slog.String("server_id", serverID.Hex()), slog.String("client_email", xuiClientEmail))

	sub.ServerID = serverID
	sub.XuiInboundID = server.TargetInboundID
	sub.XuiClientUID = xuiClientUUID
	sub.UpdatedAt = time.Now() // Update UpdatedAt time

	if err := s.subRepo.Update(ctx, sub); err != nil {
		s.logger.ErrorContext(ctx, "Failed to update subscription record after XUI client creation", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		// Attempt to delete the created X-UI client as rollback?
		// ... (rollback logic)
		return err
	}

	s.logger.InfoContext(ctx, "Subscription configured with server successfully", slog.String("sub_id", subID.Hex()), slog.String("server_id", serverID.Hex()))
	return nil
}

// GetConfigLink generates the appropriate config link (e.g., VLESS) for a configured subscription.
func (s *subscriptionService) GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error) {
	s.logger.InfoContext(ctx, "Generating config link", slog.String("sub_id", sub.ID.Hex()))

	if sub.ServerID.IsZero() {
		s.logger.WarnContext(ctx, "Cannot generate config link: Server not configured for subscription", slog.String("sub_id", sub.ID.Hex()))
		return "", apperrors.NewValidationError("Сервер для подписки еще не настроен", nil)
	}
	if sub.XuiClientUID == "" {
		s.logger.WarnContext(ctx, "Cannot generate config link: XUI client UID not set", slog.String("sub_id", sub.ID.Hex()))
		return "", apperrors.NewValidationError("Клиент X-UI для подписки не настроен", nil)
	}

	server, err := s.serverRepo.GetByID(ctx, sub.ServerID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.ErrorContext(ctx, "Server associated with subscription not found", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()))
			return "", apperrors.ErrServerNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get server details for config link", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
		return "", err
	}

	xuiClient, err := s.xuiClientFactory(server)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create xui client for config link generation", slog.String("server_id", server.ID.Hex()), slog.Any("error", err))
		return "", apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	inboundSettings, err := xuiClient.GetInbound(ctx, sub.XuiInboundID)
	if err != nil {
		if errors.Is(err, xui.ErrInboundNotFound) {
			return "", apperrors.ErrInboundNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get parsed inbound settings from x-ui", slog.String("server_id", server.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID), slog.Any("error", err))
		return "", apperrors.NewInternalError("Не удалось получить настройки подключения от сервера X-UI", err)
	}

	switch strings.ToLower(inboundSettings.Protocol) {
	case "vless":
		clientSettings := xui.ClientSettings{
			Email: sub.XuiClientUID,
			UUID:  sub.XuiClientUID,
		}
		link, err := s.generateVlessLink(server, inboundSettings, &clientSettings)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to generate VLESS link", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
			return "", err
		}
		s.logger.InfoContext(ctx, "Generated VLESS config link", slog.String("sub_id", sub.ID.Hex()))
		return link, nil
	default:
		errMsg := fmt.Sprintf("генерация ссылки для протокола '%s' не поддерживается", inboundSettings.Protocol)
		s.logger.WarnContext(ctx, errMsg, slog.String("sub_id", sub.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID))
		return "", apperrors.NewValidationError(errMsg, nil)
	}
}

// generateVlessLink constructs a VLESS configuration link.
func (s *subscriptionService) generateVlessLink(server *domain.Server, inboundSettings *xui.InboundSettings, client *xui.ClientSettings) (string, error) {
	address := server.ApiHost
	if address == "" {
		s.logger.Error("Server address (ApiHost) is empty for link generation", slog.String("server_id", server.ID.Hex()), slog.String("server_name", server.Name))
		return "", apperrors.NewInternalError(fmt.Sprintf("Адрес сервера '%s' не задан", server.Name), nil)
	}

	u := url.URL{
		Scheme: "vless",
		User:   url.User(client.UUID),
		Host:   fmt.Sprintf("%s:%d", address, inboundSettings.Port),
	}

	q := u.Query()
	q.Set("type", inboundSettings.Network)

	switch inboundSettings.Network {
	case "tcp":
	case "kcp":
	case "ws":
		if inboundSettings.WSPath != "" {
			q.Set("path", inboundSettings.WSPath)
		}
		if inboundSettings.WSHost != "" {
			q.Set("host", inboundSettings.WSHost)
		}
	case "grpc":
		if inboundSettings.GRPCService != "" {
			q.Set("serviceName", inboundSettings.GRPCService)
		}
	default:
	}

	q.Set("security", inboundSettings.Security)
	if inboundSettings.Security == "tls" {
		if inboundSettings.SNI != "" {
			q.Set("sni", inboundSettings.SNI)
		}
		if inboundSettings.Fingerprint != "" {
			q.Set("fp", inboundSettings.Fingerprint)
		}
	}
	if inboundSettings.Security == "reality" {
		if inboundSettings.PublicKey != "" {
			q.Set("pbk", inboundSettings.PublicKey)
		}
		if inboundSettings.ShortID != "" {
			q.Set("sid", inboundSettings.ShortID)
		}
		if inboundSettings.SNI != "" {
			q.Set("sni", inboundSettings.SNI)
		}
		if inboundSettings.SpiderX != "" {
			q.Set("spx", inboundSettings.SpiderX)
		}
	}

	if client.Flow != "" {
		q.Set("flow", client.Flow)
	}

	u.RawQuery = q.Encode()

	if client.Email != "" {
		u.Fragment = client.Email
	}

	return u.String(), nil
}

// FindAndExpireSubscriptions находит и обновляет статус подписок, которые истекли
func (s *subscriptionService) FindAndExpireSubscriptions(ctx context.Context) error {
	now := time.Now()
	s.logger.InfoContext(ctx, "Проверка и обновление истекших подписок", slog.Time("now", now))

	// Ищем активные подписки с истекшим сроком действия
	filter := bson.M{
		"status": domain.SubscriptionStatusActive,
		"expires_at": bson.M{
			"$lt": now, // Истекшие подписки
		},
		"auto_renew": false, // Исключаем те, которые должны автоматически продлеваться
	}

	subscriptions, err := s.subRepo.FindByFilter(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "Ошибка при поиске истекших подписок", slog.Any("error", err))
		return err
	}

	s.logger.InfoContext(ctx, "Найдены истекшие подписки", slog.Int("count", len(subscriptions)))

	// Обновляем статус каждой истекшей подписки
	for _, sub := range subscriptions {
		oldStatus := sub.Status
		sub.Status = domain.SubscriptionStatusExpired
		sub.UpdatedAt = now

		if err := s.subRepo.Update(ctx, sub); err != nil {
			s.logger.ErrorContext(ctx, "Ошибка при обновлении статуса подписки",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("old_status", string(oldStatus)),
				slog.String("new_status", string(sub.Status)),
				slog.Any("error", err))
			continue // Продолжаем с другими подписками даже если одна завершилась с ошибкой
		}

		// Возможность отключения клиента на X-UI сервере можно будет добавить позже
		// при необходимости через непосредственное взаимодействие с X-UI API

		s.logger.InfoContext(ctx, "Подписка успешно помечена как истекшая",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("user_id", sub.UserID.Hex()))
	}

	return nil
}

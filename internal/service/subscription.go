package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
	"xray-vpn-tg-bot/internal/xui"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type subscriptionService struct {
	subRepo          repository.SubscriptionRepository
	planRepo         repository.PlanRepository
	serverRepo       repository.ServerRepository
	userService      UserService
	xuiClientFactory func(server *domain.Server) (*xui.Client, error)
	logger           *slog.Logger
	userRepo         repository.UserRepository
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subRepo repository.SubscriptionRepository,
	planRepo repository.PlanRepository,
	serverRepo repository.ServerRepository,
	userService UserService,
	xuiClientFactory func(server *domain.Server) (*xui.Client, error),
	logger *slog.Logger,
	userRepo repository.UserRepository,
) SubscriptionService {
	return &subscriptionService{
		subRepo:          subRepo,
		planRepo:         planRepo,
		serverRepo:       serverRepo,
		userService:      userService,
		xuiClientFactory: xuiClientFactory,
		logger:           logger.With(slog.String("service", "subscription")),
		userRepo:         userRepo,
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

	// Генерируем UUID для клиента
	xuiClientUUID := uuid.New().String()
	// Используем комбинацию user_ID_sub_ID в качестве email
	xuiClientEmail := fmt.Sprintf("user_%s_%s", userID.Hex(), subID.Hex())

	// Получаем данные о пользователе Telegram для передачи в X-UI
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		s.logger.WarnContext(ctx, "Failed to get user details for configuration",
			slog.String("user_id", userID.Hex()),
			slog.Any("error", err))
		// Продолжаем без данных о пользователе
	}

	// Рассчитываем конкретные значения для передачи в X-UI
	expiryTimeMillis := sub.ExpiresAt.UnixMilli()
	trafficLimitBytes := sub.TrafficLimit
	trafficGB := 0
	if trafficLimitBytes > 0 {
		trafficGB = int(trafficLimitBytes / (1024 * 1024 * 1024))
	}

	s.logger.InfoContext(ctx, "Creating X-UI client",
		slog.String("server_id", serverID.Hex()),
		slog.String("client_email", xuiClientEmail),
		slog.String("client_uuid", xuiClientUUID),
		slog.Int64("expiry_time", expiryTimeMillis),
		slog.Int("traffic_gb", trafficGB))

	xuiClient, err := s.xuiClientFactory(server)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create xui client instance for configuration", slog.String("server_id", serverID.Hex()), slog.Any("error", err))
		return apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	// Получаем настройки Inbound перед созданием клиента
	inboundSettings, err := xuiClient.GetInbound(ctx, server.TargetInboundID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get inbound settings before adding client",
			slog.String("server_id", serverID.Hex()),
			slog.Int("inbound_id", server.TargetInboundID),
			slog.Any("error", err),
		)
		if errors.Is(err, xui.ErrInboundNotFound) {
			return apperrors.NewValidationError(fmt.Sprintf("Настройки подключения (Inbound ID: %d) не найдены на сервере '%s'", server.TargetInboundID, server.Name), err)
		}
		return apperrors.NewInternalError(fmt.Sprintf("Ошибка получения настроек подключения с сервера '%s'", server.Name), err)
	}
	s.logger.InfoContext(ctx, "Retrieved inbound settings", slog.Int("inbound_id", server.TargetInboundID), slog.String("protocol", inboundSettings.Protocol))

	// Формируем настройки клиента в зависимости от протокола и данных подписки
	clientSettings := xui.ClientSettings{
		Email:          xuiClientEmail,
		Enable:         true,             // Активируем клиента
		ExpiryTime:     expiryTimeMillis, // Устанавливаем время истечения
		TotalGB:        trafficGB,        // Устанавливаем лимит трафика
		SubscriptionID: subID.Hex(),      // Передаем ID подписки
	}

	// Устанавливаем TelegramID, если есть данные о пользователе
	if user != nil {
		clientSettings.TelegramID = strconv.FormatInt(user.TelegramID, 10)
	}

	// Заполняем конкретные поля в зависимости от протокола
	clientSettings.Protocol = inboundSettings.Protocol // Передаем протокол в настройки
	switch strings.ToLower(inboundSettings.Protocol) {
	case "vless", "vmess":
		clientSettings.UUID = xuiClientUUID
		// Не устанавливаем flow по умолчанию, так как это может мешать подключению
	case "trojan":
		clientSettings.Password = xuiClientUUID
	case "shadowsocks":
		clientSettings.Password = xuiClientUUID
		clientSettings.Method = "aes-256-gcm" // Устанавливаем метод шифрования по умолчанию
	case "wireguard":
		s.logger.ErrorContext(ctx, "WireGuard client creation not fully implemented", slog.Int("inbound_id", server.TargetInboundID))
		return apperrors.NewInternalError("Автоматическое создание клиента WireGuard пока не поддерживается", nil)
	default:
		errMsg := fmt.Sprintf("Неподдерживаемый протокол '%s' для Inbound ID %d на сервере '%s'", inboundSettings.Protocol, server.TargetInboundID, server.Name)
		s.logger.ErrorContext(ctx, errMsg)
		return apperrors.NewValidationError(errMsg, nil)
	}

	// Логируем информацию о настройках клиента перед отправкой
	s.logger.InfoContext(ctx, "Sending client settings to X-UI",
		slog.String("email", clientSettings.Email),
		slog.String("uuid", clientSettings.UUID),
		slog.Bool("enable", clientSettings.Enable),
		slog.Int64("expiry_time", clientSettings.ExpiryTime),
		slog.Int("total_gb", clientSettings.TotalGB),
		slog.String("protocol", clientSettings.Protocol),
		slog.String("tg_id", clientSettings.TelegramID),
		slog.String("sub_id", clientSettings.SubscriptionID))

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

	// Обновляем информацию о подписке
	sub.ServerID = serverID
	sub.XuiInboundID = server.TargetInboundID
	sub.XuiClientUID = xuiClientEmail // Используем email как идентификатор клиента
	sub.XuiClientUUID = xuiClientUUID // Сохраняем UUID клиента для дальнейшего использования
	sub.UpdatedAt = time.Now()        // Update UpdatedAt time

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

	// Получаем настройки inbound
	inboundSettings, err := xuiClient.GetInbound(ctx, sub.XuiInboundID)
	if err != nil {
		if errors.Is(err, xui.ErrInboundNotFound) {
			return "", apperrors.ErrInboundNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get parsed inbound settings from x-ui", slog.String("server_id", server.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID), slog.Any("error", err))
		return "", apperrors.NewInternalError("Не удалось получить настройки подключения от сервера X-UI", err)
	}

	// Создаем базовые настройки клиента, заполняя UUID из сохраненного в подписке значения
	clientSettings := xui.ClientSettings{
		Email: sub.XuiClientUID,
		// Если у нас есть сохраненный UUID, используем его
		UUID: sub.XuiClientUUID,
	}

	// Пытаемся получить актуальные настройки клиента с сервера, если они есть
	updatedSettings, err := xuiClient.GetClientSettings(ctx, sub.XuiInboundID, sub.XuiClientUID)
	if err != nil {
		s.logger.WarnContext(ctx, "Could not get client settings from server, using stored UUID",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("client_id", sub.XuiClientUID),
			slog.String("stored_uuid", sub.XuiClientUUID),
			slog.Any("error", err))
		// Продолжаем с сохраненным UUID, так как актуальные настройки получить не удалось
	} else if updatedSettings != nil {
		// Если настройки успешно получены, используем их
		clientSettings = *updatedSettings
		s.logger.InfoContext(ctx, "Retrieved client settings from server",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("uuid", clientSettings.UUID))

		// Если UUID в подписке и на сервере не совпадают, обновляем его в подписке
		if sub.XuiClientUUID != clientSettings.UUID && clientSettings.UUID != "" {
			s.logger.InfoContext(ctx, "Updating stored UUID in subscription",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("old_uuid", sub.XuiClientUUID),
				slog.String("new_uuid", clientSettings.UUID))
			sub.XuiClientUUID = clientSettings.UUID
			if err := s.subRepo.Update(ctx, sub); err != nil {
				s.logger.WarnContext(ctx, "Failed to update client UUID in subscription",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				// Продолжаем выполнение даже в случае ошибки обновления
			}
		}
	}

	// Проверяем, есть ли UUID в настройках клиента
	if clientSettings.UUID == "" {
		s.logger.ErrorContext(ctx, "Client UUID is missing for config link generation",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("client_id", sub.XuiClientUID))
		return "", apperrors.NewInternalError("UUID клиента не найден для генерации ссылки", nil)
	}

	switch strings.ToLower(inboundSettings.Protocol) {
	case "vless":
		// Генерируем VLESS ссылку
		link, err := s.generateVlessLink(server, inboundSettings, &clientSettings)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to generate VLESS link", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
			return "", err
		}
		s.logger.InfoContext(ctx, "Generated VLESS config link",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("link", link))
		return link, nil
	default:
		errMsg := fmt.Sprintf("генерация ссылки для протокола '%s' не поддерживается", inboundSettings.Protocol)
		s.logger.WarnContext(ctx, errMsg, slog.String("sub_id", sub.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID))
		return "", apperrors.NewValidationError(errMsg, nil)
	}
}

// generateVlessLink constructs a VLESS configuration link.
func (s *subscriptionService) generateVlessLink(server *domain.Server, inboundSettings *xui.InboundSettings, clientSettings *xui.ClientSettings) (string, error) {
	address := server.PublicHost
	if address == "" {
		s.logger.Error("Server PublicHost is empty for link generation", slog.String("server_id", server.ID.Hex()), slog.String("server_name", server.Name))
		return "", apperrors.NewInternalError(fmt.Sprintf("Публичный адрес сервера '%s' не задан", server.Name), nil)
	}

	// Очищаем адрес от возможных протоколов и портов
	address = strings.TrimPrefix(address, "http://")
	address = strings.TrimPrefix(address, "https://")

	// Удаляем порт из адреса, если он там есть
	if idx := strings.Index(address, ":"); idx != -1 {
		address = address[:idx]
	}

	// Проверяем наличие UUID (обязательное поле для VLESS)
	if clientSettings.UUID == "" {
		s.logger.Error("Client UUID is empty for VLESS link generation",
			slog.String("client_email", clientSettings.Email))
		return "", apperrors.NewInternalError("UUID клиента не задан для генерации VLESS ссылки", nil)
	}

	// Используем UUID вместо email для части идентификации клиента
	u := url.URL{
		Scheme: "vless",
		User:   url.User(clientSettings.UUID), // Используем UUID вместо email
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

	// Добавляем flow только если он не "none" и не пустой
	// "none" - это значение по умолчанию, его не нужно указывать
	if clientSettings.Flow != "" && clientSettings.Flow != "none" {
		q.Set("flow", clientSettings.Flow)
	}

	u.RawQuery = q.Encode()

	// Используем email для имени клиента (фрагмент URL)
	if clientSettings.Email != "" {
		u.Fragment = clientSettings.Email
	}

	s.logger.InfoContext(context.Background(), "Generated VLESS link with UUID",
		slog.String("uuid", clientSettings.UUID),
		slog.String("email", clientSettings.Email))

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

// GetSubscriptionQRCode получает QR-код для конфигурации клиента
func (s *subscriptionService) GetSubscriptionQRCode(ctx context.Context, sub *domain.Subscription) ([]byte, error) {
	if sub.ServerID.IsZero() {
		return nil, fmt.Errorf("subscription %s does not have a server configured", sub.ID.Hex())
	}

	if sub.XuiClientUID == "" {
		s.logger.WarnContext(ctx, "Cannot generate QR code: XUI client UID not set", slog.String("sub_id", sub.ID.Hex()))
		return nil, apperrors.NewValidationError("Клиент X-UI для подписки не настроен", nil)
	}

	// Получаем данные сервера
	server, err := s.serverRepo.GetByID(ctx, sub.ServerID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.ErrorContext(ctx, "Server associated with subscription not found", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()))
			return nil, apperrors.ErrServerNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get server details for QR code", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
		return nil, err
	}

	xuiClient, err := s.xuiClientFactory(server)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create xui client for QR code generation", slog.String("server_id", server.ID.Hex()), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	// Получаем информацию об inbound для определения протокола
	inboundSettings, err := xuiClient.GetInbound(ctx, sub.XuiInboundID)
	if err != nil {
		if errors.Is(err, xui.ErrInboundNotFound) {
			return nil, apperrors.ErrInboundNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get inbound settings from x-ui", slog.String("server_id", server.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить настройки подключения от сервера X-UI", err)
	}

	// Получаем настройки клиента для подтверждения актуального UUID
	clientSettings, err := xuiClient.GetClientSettings(ctx, sub.XuiInboundID, sub.XuiClientUID)
	if err != nil || clientSettings == nil {
		// Если не удалось получить настройки клиента, используем сохраненный UUID
		if sub.XuiClientUUID != "" {
			s.logger.InfoContext(ctx, "Using stored UUID for QR code generation",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("stored_uuid", sub.XuiClientUUID))

			// Для генерации QR-кода нам нужна ссылка, так что получаем её
			configLink, err := s.GetConfigLink(ctx, sub)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to generate config link for QR code",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				return nil, err
			}

			// Создаем QR-код локально, так как не можем получить его от сервера
			qrCode, err := generateQRCode(configLink)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to generate local QR code",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				return nil, err
			}

			s.logger.InfoContext(ctx, "Generated local QR code successfully",
				slog.String("sub_id", sub.ID.Hex()),
				slog.Int("size_bytes", len(qrCode)))
			return qrCode, nil
		} else {
			s.logger.ErrorContext(ctx, "Failed to get client settings and no stored UUID",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("client_id", sub.XuiClientUID),
				slog.Any("error", err))
			return nil, fmt.Errorf("не удалось получить настройки клиента и нет сохраненного UUID: %w", err)
		}
	}

	// Сверяем UUID, если в подписке сохранен неверный - обновляем его
	if clientSettings.UUID != "" && clientSettings.UUID != sub.XuiClientUUID {
		s.logger.InfoContext(ctx, "Updating stored UUID in subscription during QR code generation",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("old_uuid", sub.XuiClientUUID),
			slog.String("new_uuid", clientSettings.UUID))
		sub.XuiClientUUID = clientSettings.UUID
		if err := s.subRepo.Update(ctx, sub); err != nil {
			s.logger.WarnContext(ctx, "Failed to update client UUID in subscription during QR code generation",
				slog.String("sub_id", sub.ID.Hex()),
				slog.Any("error", err))
			// Продолжаем выполнение даже в случае ошибки обновления
		}
	}

	// Теперь создаем QR-код локально по ссылке
	configLink, err := s.GetConfigLink(ctx, sub)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to generate config link for local QR code",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
		return nil, err
	}

	qrCode, err := generateQRCode(configLink)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to generate local QR code",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))

		// Если локальная генерация не удалась, попробуем получить от сервера
		qrCode, err := xuiClient.GetClientQRCode(ctx, sub.XuiClientUID, inboundSettings.Protocol)
		if err != nil {
			s.logger.ErrorContext(ctx, "Both local and remote QR code generation failed",
				slog.String("server_id", server.ID.Hex()),
				slog.String("client_id", sub.XuiClientUID),
				slog.Any("error", err))
			return nil, fmt.Errorf("не удалось создать QR-код ни локально, ни на сервере: %w", err)
		}
		return qrCode, nil
	}

	s.logger.InfoContext(ctx, "Generated local QR code successfully",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int("size_bytes", len(qrCode)))
	return qrCode, nil
}

// generateQRCode генерирует QR-код локально на основе ссылки конфигурации
func generateQRCode(link string) ([]byte, error) {
	// Используем библиотеку go-qrcode для создания QR-кода
	qr, err := qrcode.Encode(link, qrcode.Medium, 256)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации QR-кода: %w", err)
	}

	return qr, nil
}

// CreateSubscription создает новую подписку для пользователя
func (s *subscriptionService) CreateSubscription(ctx context.Context, userID primitive.ObjectID, planID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.InfoContext(ctx, "Creating new subscription", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during subscription creation", slog.String("plan_id", planID.Hex()))
			return nil, err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription creation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return nil, err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)

	// Создаем новую подписку
	newSub := &domain.Subscription{
		ID:           primitive.NewObjectID(),
		UserID:       userID,
		PlanID:       planID,
		Status:       domain.SubscriptionStatusActive,
		ActivatedAt:  now,
		ExpiresAt:    newExpiryDate,
		AutoRenew:    false,
		CreatedAt:    now,
		UpdatedAt:    now,
		TrafficLimit: int64(plan.TrafficGB) * 1024 * 1024 * 1024,
		TrafficUsed:  0,
	}

	if err := s.subRepo.Create(ctx, newSub); err != nil {
		s.logger.ErrorContext(ctx, "Failed to create new subscription record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, err
	}

	s.logger.InfoContext(ctx, "New subscription created successfully", slog.String("sub_id", newSub.ID.Hex()))
	return newSub, nil
}

// GetActiveSubscriptionForUser получает список активных подписок для пользователя
func (s *subscriptionService) GetActiveSubscriptionForUser(ctx context.Context, userID primitive.ObjectID) ([]*domain.SubscriptionDetails, error) {
	s.logger.DebugContext(ctx, "Getting active subscriptions for user", slog.String("user_id", userID.Hex()))

	// Получаем активную подписку
	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			s.logger.DebugContext(ctx, "No active subscriptions found for user", slog.String("user_id", userID.Hex()))
			return []*domain.SubscriptionDetails{}, nil
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}

	// Если подписка найдена, создаем детали подписки
	if sub != nil {
		// Получаем план
		plan, err := s.planRepo.GetByID(ctx, sub.PlanID)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get plan for subscription details", slog.String("sub_id", sub.ID.Hex()), slog.String("plan_id", sub.PlanID.Hex()), slog.Any("error", err))
			// Продолжаем даже без данных о плане
		}

		// Получаем сервер, если он настроен
		var serverName string
		if !sub.ServerID.IsZero() {
			server, err := s.serverRepo.GetByID(ctx, sub.ServerID)
			if err == nil && server != nil {
				serverName = server.Name
			}
		}

		// Создаем объект с деталями
		details := &domain.SubscriptionDetails{
			Subscription: sub,
			PlanName:     "Неизвестный план",
			ServerName:   serverName,
		}

		// Устанавливаем имя плана, если план был найден
		if plan != nil {
			details.PlanName = plan.Name
		}

		return []*domain.SubscriptionDetails{details}, nil
	}

	return []*domain.SubscriptionDetails{}, nil
}

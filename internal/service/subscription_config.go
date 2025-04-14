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
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// subscriptionConfigurator отвечает за конфигурацию подписок на серверах X-UI.
type subscriptionConfigurator struct {
	subRepo          repository.SubscriptionRepository
	serverRepo       repository.ServerRepository
	userRepo         repository.UserRepository
	xuiClientFactory func(server *domain.Server) (*xui.Client, error)
	logger           *slog.Logger
}

// newSubscriptionConfigurator создает новый экземпляр subscriptionConfigurator.
func newSubscriptionConfigurator(
	subRepo repository.SubscriptionRepository,
	serverRepo repository.ServerRepository,
	userRepo repository.UserRepository,
	xuiClientFactory func(server *domain.Server) (*xui.Client, error),
	logger *slog.Logger,
) *subscriptionConfigurator {
	return &subscriptionConfigurator{
		subRepo:          subRepo,
		serverRepo:       serverRepo,
		userRepo:         userRepo,
		xuiClientFactory: xuiClientFactory,
		logger:           logger.With(slog.String("component", "subscription_configurator")),
	}
}

// ConfigureSubscriptionServer links a subscription to a server and creates the X-UI client.
func (c *subscriptionConfigurator) ConfigureSubscriptionServer(ctx context.Context, userID, subID, serverID primitive.ObjectID) error {
	c.logger.InfoContext(ctx, "Configuring server for subscription", slog.String("user_id", userID.Hex()), slog.String("sub_id", subID.Hex()), slog.String("server_id", serverID.Hex()))

	sub, err := c.subRepo.GetByID(ctx, subID) // Use GetByID for direct access
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			c.logger.WarnContext(ctx, "Subscription not found by ID during configuration", slog.String("sub_id", subID.Hex()))
			return apperrors.ErrSubscriptionNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get subscription by ID from repository during configuration", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		return err // Return the original error
	}

	if sub.UserID != userID {
		c.logger.WarnContext(ctx, "User attempted to configure subscription they don't own", slog.String("user_id", userID.Hex()), slog.String("sub_id", subID.Hex()))
		return apperrors.ErrForbidden
	}
	if sub.Status != domain.SubscriptionStatusActive {
		c.logger.WarnContext(ctx, "Attempted to configure non-active subscription", slog.String("sub_id", subID.Hex()), slog.String("status", string(sub.Status)))
		return apperrors.ErrSubscriptionInactive
	}
	if !sub.ServerID.IsZero() {
		c.logger.WarnContext(ctx, "Attempted to re-configure server for subscription", slog.String("sub_id", subID.Hex()), slog.String("current_server_id", sub.ServerID.Hex()))
		return apperrors.ErrSubscriptionConfigured
	}

	server, err := c.serverRepo.GetByID(ctx, serverID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			c.logger.WarnContext(ctx, "Selected server not found", slog.String("server_id", serverID.Hex()))
			return apperrors.ErrServerNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get server details for configuration", slog.String("server_id", serverID.Hex()), slog.Any("error", err))
		return err
	}
	if !server.IsEnabled {
		errMsg := fmt.Sprintf("Выбранный сервер '%s' недоступен", server.Name)
		c.logger.WarnContext(ctx, errMsg, slog.String("server_id", serverID.Hex()))
		return apperrors.NewValidationError(errMsg, nil)
	}

	// Генерируем UUID для клиента
	xuiClientUUID := uuid.New().String()
	// Используем комбинацию user_ID_sub_ID в качестве email
	xuiClientEmail := fmt.Sprintf("user_%s_%s", userID.Hex(), subID.Hex())

	// Получаем данные о пользователе Telegram для передачи в X-UI
	user, err := c.userRepo.GetByID(ctx, userID)
	if err != nil {
		c.logger.WarnContext(ctx, "Failed to get user details for configuration",
			slog.String("user_id", userID.Hex()),
			slog.Any("error", err))
		// Продолжаем без данных о пользователе
	}

	// Рассчитываем конкретные значения для передачи в X-UI
	expiryTimeMillis := sub.ExpiresAt.UnixMilli()
	trafficLimitBytes := sub.TrafficLimit // Already in bytes

	c.logger.InfoContext(ctx, "Creating X-UI client",
		slog.String("server_id", serverID.Hex()),
		slog.String("client_email", xuiClientEmail),
		slog.String("client_uuid", xuiClientUUID),
		slog.Int64("expiry_time", expiryTimeMillis),
		slog.Int64("traffic_bytes", trafficLimitBytes))

	xuiClient, err := c.xuiClientFactory(server)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to create xui client instance for configuration", slog.String("server_id", serverID.Hex()), slog.Any("error", err))
		return apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	// Получаем настройки Inbound перед созданием клиента
	inboundSettings, err := xuiClient.GetInbound(ctx, server.TargetInboundID)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to get inbound settings before adding client",
			slog.String("server_id", serverID.Hex()),
			slog.Int("inbound_id", server.TargetInboundID),
			slog.Any("error", err),
		)
		if errors.Is(err, xui.ErrInboundNotFound) {
			return apperrors.NewValidationError(fmt.Sprintf("Настройки подключения (Inbound ID: %d) не найдены на сервере '%s'", server.TargetInboundID, server.Name), err)
		}
		return apperrors.NewInternalError(fmt.Sprintf("Ошибка получения настроек подключения с сервера '%s'", server.Name), err)
	}
	c.logger.InfoContext(ctx, "Retrieved inbound settings", slog.Int("inbound_id", server.TargetInboundID), slog.String("protocol", inboundSettings.Protocol))

	// Формируем настройки клиента в зависимости от протокола и данных подписки
	clientSettings := xui.ClientSettings{
		Email:          xuiClientEmail,
		Enable:         true,              // Активируем клиента
		ExpiryTime:     expiryTimeMillis,  // Устанавливаем время истечения
		TotalBytes:     trafficLimitBytes, // Устанавливаем лимит трафика в байтах
		SubscriptionID: subID.Hex(),       // Передаем ID подписки
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
		c.logger.ErrorContext(ctx, "WireGuard client creation not fully implemented", slog.Int("inbound_id", server.TargetInboundID))
		return apperrors.NewInternalError("Автоматическое создание клиента WireGuard пока не поддерживается", nil)
	default:
		errMsg := fmt.Sprintf("Неподдерживаемый протокол '%s' для Inbound ID %d на сервере '%s'", inboundSettings.Protocol, server.TargetInboundID, server.Name)
		c.logger.ErrorContext(ctx, errMsg)
		return apperrors.NewValidationError(errMsg, nil)
	}

	// Логируем информацию о настройках клиента перед отправкой
	c.logger.InfoContext(ctx, "Sending client settings to X-UI",
		slog.String("email", clientSettings.Email),
		slog.String("uuid", clientSettings.UUID),
		slog.Bool("enable", clientSettings.Enable),
		slog.Int64("expiry_time", clientSettings.ExpiryTime),
		slog.Int64("total_bytes", clientSettings.TotalBytes),
		slog.String("protocol", clientSettings.Protocol),
		slog.String("tg_id", clientSettings.TelegramID),
		slog.String("sub_id", clientSettings.SubscriptionID))

	err = xuiClient.AddClient(ctx, server.TargetInboundID, clientSettings)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to add client to x-ui",
			slog.String("server_id", serverID.Hex()),
			slog.Int("inbound_id", server.TargetInboundID),
			slog.String("client_email", xuiClientEmail),
			slog.Any("error", err),
		)

		// Проверяем, является ли ошибка дублированием email
		if strings.Contains(err.Error(), "Duplicate email") {
			c.logger.InfoContext(ctx, "Detected duplicate email error, trying to update existing client instead",
				slog.String("client_email", xuiClientEmail))

			// Получаем данные об inbound для поиска клиента по email
			inbound, inboundErr := xuiClient.GetInbound(ctx, server.TargetInboundID)
			if inboundErr != nil {
				c.logger.ErrorContext(ctx, "Failed to get inbound for client search",
					slog.String("server_id", serverID.Hex()),
					slog.Int("inbound_id", server.TargetInboundID),
					slog.Any("error", inboundErr))
				return apperrors.NewInternalError(fmt.Sprintf("Ошибка получения данных с сервера X-UI %s", server.Name), inboundErr)
			}

			// Ищем клиента с указанным email
			var foundClient *xui.ClientSettings
			for _, client := range inbound.Clients {
				if client.Email == xuiClientEmail {
					foundClient = &client
					c.logger.InfoContext(ctx, "Found existing client with the same email",
						slog.String("client_email", xuiClientEmail),
						slog.String("client_uuid", client.UUID))
					break
				}
			}

			if foundClient != nil {
				// Обновляем найденного клиента
				foundClient.ExpiryTime = clientSettings.ExpiryTime
				foundClient.TotalBytes = clientSettings.TotalBytes
				foundClient.TelegramID = clientSettings.TelegramID
				foundClient.SubscriptionID = clientSettings.SubscriptionID

				updateErr := xuiClient.UpdateClient(ctx, server.TargetInboundID, foundClient.UUID, *foundClient)
				if updateErr != nil {
					c.logger.ErrorContext(ctx, "Failed to update existing client",
						slog.String("client_email", xuiClientEmail),
						slog.String("client_uuid", foundClient.UUID),
						slog.Any("error", updateErr))
					return apperrors.NewInternalError(fmt.Sprintf("Ошибка обновления клиента на сервере X-UI %s", server.Name), updateErr)
				}

				c.logger.InfoContext(ctx, "Successfully updated existing client instead of creating a new one",
					slog.String("client_email", xuiClientEmail),
					slog.String("client_uuid", foundClient.UUID))

				// Обновляем информацию о подписке с найденными данными
				sub.ServerID = serverID
				sub.XuiInboundID = server.TargetInboundID
				sub.XuiClientUID = xuiClientEmail    // Используем email как идентификатор клиента
				sub.XuiClientUUID = foundClient.UUID // Используем UUID найденного клиента
				sub.UpdatedAt = time.Now()

				if err := c.subRepo.Update(ctx, sub); err != nil {
					c.logger.ErrorContext(ctx, "Failed to update subscription with found client data",
						slog.String("sub_id", subID.Hex()),
						slog.Any("error", err))
					return err
				}

				c.logger.InfoContext(ctx, "Subscription linked to existing client successfully",
					slog.String("sub_id", subID.Hex()),
					slog.String("server_id", serverID.Hex()),
					slog.String("client_uuid", foundClient.UUID))
				return nil
			}

			// Если клиент не найден, продолжаем с обычной ошибкой
			c.logger.ErrorContext(ctx, "Could not find client with duplicate email",
				slog.String("client_email", xuiClientEmail))
		}

		return apperrors.NewInternalError(fmt.Sprintf("Ошибка создания клиента на сервере X-UI %s", server.Name), err)
	}
	c.logger.InfoContext(ctx, "X-UI client created successfully", slog.String("server_id", serverID.Hex()), slog.String("client_email", xuiClientEmail))

	// Обновляем информацию о подписке
	sub.ServerID = serverID
	sub.XuiInboundID = server.TargetInboundID
	sub.XuiClientUID = xuiClientEmail // Используем email как идентификатор клиента
	sub.XuiClientUUID = xuiClientUUID // Сохраняем UUID клиента для дальнейшего использования
	sub.UpdatedAt = time.Now()        // Update UpdatedAt time

	if err := c.subRepo.Update(ctx, sub); err != nil {
		c.logger.ErrorContext(ctx, "Failed to update subscription record after XUI client creation", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		// Attempt to delete the created X-UI client as rollback?
		// ... (rollback logic)
		return err
	}

	c.logger.InfoContext(ctx, "Subscription configured with server successfully", slog.String("sub_id", subID.Hex()), slog.String("server_id", serverID.Hex()))
	return nil
}

// GetConfigLink generates the appropriate config link (e.g., VLESS) for a configured subscription.
func (c *subscriptionConfigurator) GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error) {
	c.logger.InfoContext(ctx, "Generating config link", slog.String("sub_id", sub.ID.Hex()))

	if sub.ServerID.IsZero() {
		c.logger.WarnContext(ctx, "Cannot generate config link: Server not configured for subscription", slog.String("sub_id", sub.ID.Hex()))
		return "", apperrors.NewValidationError("Сервер для подписки еще не настроен", nil)
	}
	if sub.XuiClientUID == "" {
		c.logger.WarnContext(ctx, "Cannot generate config link: XUI client UID not set", slog.String("sub_id", sub.ID.Hex()))
		return "", apperrors.NewValidationError("Клиент X-UI для подписки не настроен", nil)
	}

	server, err := c.serverRepo.GetByID(ctx, sub.ServerID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			c.logger.ErrorContext(ctx, "Server associated with subscription not found", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()))
			return "", apperrors.ErrServerNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get server details for config link", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
		return "", err
	}

	xuiClient, err := c.xuiClientFactory(server)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to create xui client for config link generation", slog.String("server_id", server.ID.Hex()), slog.Any("error", err))
		return "", apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	// Получаем настройки inbound
	inboundSettings, err := xuiClient.GetInbound(ctx, sub.XuiInboundID)
	if err != nil {
		if errors.Is(err, xui.ErrInboundNotFound) {
			return "", apperrors.ErrInboundNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get parsed inbound settings from x-ui", slog.String("server_id", server.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID), slog.Any("error", err))
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
		c.logger.WarnContext(ctx, "Could not get client settings from server, using stored UUID",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("client_id", sub.XuiClientUID),
			slog.String("stored_uuid", sub.XuiClientUUID),
			slog.Any("error", err))
		// Продолжаем с сохраненным UUID, так как актуальные настройки получить не удалось
	} else if updatedSettings != nil {
		// Если настройки успешно получены, используем их
		clientSettings = *updatedSettings
		c.logger.InfoContext(ctx, "Retrieved client settings from server",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("uuid", clientSettings.UUID))

		// Если UUID в подписке и на сервере не совпадают, обновляем его в подписке
		if sub.XuiClientUUID != clientSettings.UUID && clientSettings.UUID != "" {
			c.logger.InfoContext(ctx, "Updating stored UUID in subscription",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("old_uuid", sub.XuiClientUUID),
				slog.String("new_uuid", clientSettings.UUID))
			sub.XuiClientUUID = clientSettings.UUID
			if err := c.subRepo.Update(ctx, sub); err != nil {
				c.logger.WarnContext(ctx, "Failed to update client UUID in subscription",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				// Продолжаем выполнение даже в случае ошибки обновления
			}
		}
	}

	// Проверяем, есть ли UUID в настройках клиента
	if clientSettings.UUID == "" {
		c.logger.ErrorContext(ctx, "Client UUID is missing for config link generation",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("client_id", sub.XuiClientUID))
		return "", apperrors.NewInternalError("UUID клиента не найден для генерации ссылки", nil)
	}

	switch strings.ToLower(inboundSettings.Protocol) {
	case "vless":
		// Генерируем VLESS ссылку
		link, err := c.generateVlessLink(server, inboundSettings, &clientSettings)
		if err != nil {
			c.logger.ErrorContext(ctx, "Failed to generate VLESS link", slog.String("sub_id", sub.ID.Hex()), slog.Any("error", err))
			return "", err
		}
		c.logger.InfoContext(ctx, "Generated VLESS config link",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("link", link))
		return link, nil
	default:
		errMsg := fmt.Sprintf("генерация ссылки для протокола '%s' не поддерживается", inboundSettings.Protocol)
		c.logger.WarnContext(ctx, errMsg, slog.String("sub_id", sub.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID))
		return "", apperrors.NewValidationError(errMsg, nil)
	}
}

// generateVlessLink constructs a VLESS configuration link.
func (c *subscriptionConfigurator) generateVlessLink(server *domain.Server, inboundSettings *xui.InboundSettings, clientSettings *xui.ClientSettings) (string, error) {
	address := server.PublicHost
	if address == "" {
		c.logger.Error("Server PublicHost is empty for link generation", slog.String("server_id", server.ID.Hex()), slog.String("server_name", server.Name))
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
		c.logger.Error("Client UUID is empty for VLESS link generation",
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

	c.logger.InfoContext(context.Background(), "Generated VLESS link with UUID",
		slog.String("uuid", clientSettings.UUID),
		slog.String("email", clientSettings.Email))

	return u.String(), nil
}

// GetSubscriptionQRCode получает QR-код для конфигурации клиента
func (c *subscriptionConfigurator) GetSubscriptionQRCode(ctx context.Context, sub *domain.Subscription) ([]byte, error) {
	if sub.ServerID.IsZero() {
		return nil, fmt.Errorf("subscription %s does not have a server configured", sub.ID.Hex())
	}

	if sub.XuiClientUID == "" {
		c.logger.WarnContext(ctx, "Cannot generate QR code: XUI client UID not set", slog.String("sub_id", sub.ID.Hex()))
		return nil, apperrors.NewValidationError("Клиент X-UI для подписки не настроен", nil)
	}

	// Получаем данные сервера
	server, err := c.serverRepo.GetByID(ctx, sub.ServerID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			c.logger.ErrorContext(ctx, "Server associated with subscription not found", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()))
			return nil, apperrors.ErrServerNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get server details for QR code", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
		return nil, err
	}

	xuiClient, err := c.xuiClientFactory(server)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to create xui client for QR code generation", slog.String("server_id", server.ID.Hex()), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось подключиться к серверу X-UI", err)
	}

	// Получаем информацию об inbound для определения протокола
	inboundSettings, err := xuiClient.GetInbound(ctx, sub.XuiInboundID)
	if err != nil {
		if errors.Is(err, xui.ErrInboundNotFound) {
			return nil, apperrors.ErrInboundNotFound
		}
		c.logger.ErrorContext(ctx, "Failed to get inbound settings from x-ui", slog.String("server_id", server.ID.Hex()), slog.Int("inbound_id", sub.XuiInboundID), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить настройки подключения от сервера X-UI", err)
	}

	// Получаем настройки клиента для подтверждения актуального UUID
	clientSettings, err := xuiClient.GetClientSettings(ctx, sub.XuiInboundID, sub.XuiClientUID)
	if err != nil || clientSettings == nil {
		// Если не удалось получить настройки клиента, используем сохраненный UUID
		if sub.XuiClientUUID != "" {
			c.logger.InfoContext(ctx, "Using stored UUID for QR code generation",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("stored_uuid", sub.XuiClientUUID))

			// Для генерации QR-кода нам нужна ссылка, так что получаем её
			configLink, err := c.GetConfigLink(ctx, sub)
			if err != nil {
				c.logger.ErrorContext(ctx, "Failed to generate config link for QR code",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				return nil, err
			}

			// Создаем QR-код локально, так как не можем получить его от сервера
			qrCode, err := c.generateQRCode(configLink)
			if err != nil {
				c.logger.ErrorContext(ctx, "Failed to generate local QR code",
					slog.String("sub_id", sub.ID.Hex()),
					slog.Any("error", err))
				return nil, err
			}

			c.logger.InfoContext(ctx, "Generated local QR code successfully",
				slog.String("sub_id", sub.ID.Hex()),
				slog.Int("size_bytes", len(qrCode)))
			return qrCode, nil
		} else {
			c.logger.ErrorContext(ctx, "Failed to get client settings and no stored UUID",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("client_id", sub.XuiClientUID),
				slog.Any("error", err))
			return nil, fmt.Errorf("не удалось получить настройки клиента и нет сохраненного UUID: %w", err)
		}
	}

	// Сверяем UUID, если в подписке сохранен неверный - обновляем его
	if clientSettings.UUID != "" && clientSettings.UUID != sub.XuiClientUUID {
		c.logger.InfoContext(ctx, "Updating stored UUID in subscription during QR code generation",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("old_uuid", sub.XuiClientUUID),
			slog.String("new_uuid", clientSettings.UUID))
		sub.XuiClientUUID = clientSettings.UUID
		if err := c.subRepo.Update(ctx, sub); err != nil {
			c.logger.WarnContext(ctx, "Failed to update client UUID in subscription during QR code generation",
				slog.String("sub_id", sub.ID.Hex()),
				slog.Any("error", err))
			// Продолжаем выполнение даже в случае ошибки обновления
		}
	}

	// Теперь создаем QR-код локально по ссылке
	configLink, err := c.GetConfigLink(ctx, sub)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to generate config link for local QR code",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
		return nil, err
	}

	qrCode, err := c.generateQRCode(configLink)
	if err != nil {
		c.logger.ErrorContext(ctx, "Failed to generate local QR code",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))

		// Если локальная генерация не удалась, попробуем получить от сервера
		qrCode, err := xuiClient.GetClientQRCode(ctx, sub.XuiClientUID, inboundSettings.Protocol)
		if err != nil {
			c.logger.ErrorContext(ctx, "Both local and remote QR code generation failed",
				slog.String("server_id", server.ID.Hex()),
				slog.String("client_id", sub.XuiClientUID),
				slog.Any("error", err))
			return nil, fmt.Errorf("не удалось создать QR-код ни локально, ни на сервере: %w", err)
		}
		return qrCode, nil
	}

	c.logger.InfoContext(ctx, "Generated local QR code successfully",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int("size_bytes", len(qrCode)))
	return qrCode, nil
}

// generateQRCode генерирует QR-код локально на основе ссылки конфигурации
func (c *subscriptionConfigurator) generateQRCode(link string) ([]byte, error) {
	// Используем библиотеку go-qrcode для создания QR-кода
	qr, err := qrcode.Encode(link, qrcode.Medium, 256)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации QR-кода: %w", err)
	}

	return qr, nil
}

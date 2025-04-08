package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// AddClient adds a new client to a specific inbound.
func (c *Client) AddClient(ctx context.Context, inboundID int, client ClientSettings) error {
	endpoint := path.Join(c.apiPath, "addClient")
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	// Формируем объект с настройками клиента
	clientSettings := map[string]interface{}{
		"email":  client.Email,
		"enable": true,                  // Всегда true для новых клиентов
		"tgId":   client.TelegramID,     // Добавляем TelegramID
		"subId":  client.SubscriptionID, // Добавляем SubscriptionID
	}

	// Добавляем поля только если они заданы
	if client.ExpiryTime > 0 {
		clientSettings["expiryTime"] = client.ExpiryTime
		c.logger.InfoContext(ctx, "Setting expiry time for client",
			slog.String("email", client.Email),
			slog.Int64("expiry_time", client.ExpiryTime))
	}

	if client.TotalGB > 0 {
		// В API 3x-ui параметр totalGB должен быть в ГБ
		clientSettings["totalGB"] = client.TotalGB
		c.logger.InfoContext(ctx, "Setting traffic limit for client",
			slog.String("email", client.Email),
			slog.Int("total_gb", client.TotalGB))
	} else {
		// Если трафик не задан, устанавливаем "неограниченный" трафик (большое значение)
		// 1073741824 GB = 1 ПБ (Петабайт) - практически неограниченный трафик
		clientSettings["totalGB"] = 1073741824
		c.logger.InfoContext(ctx, "Setting unlimited traffic for client (1 PB)",
			slog.String("email", client.Email))
	}

	// Добавляем ID клиента и другие протоколо-зависимые поля
	switch strings.ToLower(client.Protocol) {
	case "vless", "vmess":
		clientSettings["id"] = client.UUID // ID для VMESS/VLESS (в 3x-ui API используется "id", а не "uuid")
		if client.Flow != "" {
			clientSettings["flow"] = client.Flow
		} else {
			clientSettings["flow"] = "none" // Явно указываем flow по умолчанию
		}
	case "trojan":
		clientSettings["password"] = client.UUID // Используем UUID как пароль
	case "shadowsocks":
		clientSettings["password"] = client.UUID // Используем UUID как пароль
		if client.Method != "" {
			clientSettings["method"] = client.Method
		}
	case "wireguard":
		// Добавить поля WG: publicKey, privateKey, presharedKey, allowedIPs, clientAddress
		c.logger.WarnContext(ctx, "AddClient for WireGuard might require more fields", slog.Int("inbound_id", inboundID))
	default:
		c.logger.WarnContext(ctx, "Unknown or missing protocol for client settings, sending basic fields",
			slog.Int("inbound_id", inboundID),
			slog.String("email", client.Email))
		if client.UUID != "" {
			clientSettings["id"] = client.UUID
		}
	}

	// Создаем правильную структуру запроса: settings должен содержать массив clients
	settingsObj := map[string]interface{}{
		"clients": []interface{}{clientSettings},
	}

	// Маршалим настройки клиента в JSON-строку
	settingsJSON, err := json.Marshal(settingsObj)
	if err != nil {
		return fmt.Errorf("failed to marshal client settings: %w", err)
	}

	// Создаем основной запрос, где settings - это СТРОКА
	reqPayload := map[string]interface{}{
		"id":       inboundID,            // ID инбаунда как число
		"settings": string(settingsJSON), // Настройки клиента как СТРОКА JSON
	}

	// Маршалим весь запрос в JSON
	jsonBody, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal add client request: %w", err)
	}

	// Логируем тело запроса перед отправкой
	c.logger.Debug("Sending AddClient request body",
		slog.String("body", string(jsonBody)),
		slog.String("email", client.Email),
		slog.String("uuid", client.UUID))

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), bytes.NewBuffer(jsonBody))
	if err != nil {
		// Для ошибок 500 пытаемся прочитать ответ сервера для более детальной диагностики
		if xerr, ok := err.(interface{ Unwrap() error }); ok {
			if reqErr, ok := xerr.Unwrap().(*url.Error); ok && reqErr != nil {
				c.logger.Error("Detailed error info for API request",
					slog.String("url_error", reqErr.Error()),
					slog.String("op", reqErr.Op),
					slog.String("url", reqErr.URL))
			}
		}
		return err
	}
	defer resp.Body.Close()

	// Читаем тело ответа полностью
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Декодируем как GenericResponse
	var genericResp GenericResponse
	err = json.Unmarshal(respBody, &genericResp)
	if err != nil {
		return fmt.Errorf("failed to decode response: %w, body: %s", err, string(respBody))
	}

	// Проверяем успешность операции
	if genericResp.Success {
		c.logger.Info("Successfully added x-ui client",
			slog.Int("inbound_id", inboundID),
			slog.String("client_email", client.Email),
			slog.Int("traffic_gb", client.TotalGB),
			slog.Int64("expiry_time", client.ExpiryTime))
		return nil
	}

	// В случае ошибки
	errMsg := genericResp.Msg
	if errMsg == "" {
		errMsg = "unknown error from 3x-ui response"
	}
	c.logger.Error("Failed to add x-ui client",
		slog.Int("inbound_id", inboundID),
		slog.String("client_email", client.Email),
		slog.String("msg", errMsg),
		slog.String("raw_body", string(respBody)))
	return fmt.Errorf("%w: %s", ErrOperationFailed, errMsg)
}

// UpdateClient updates an existing client within a specific inbound.
// This endpoint is often used to enable/disable clients, update expiry, traffic, etc.
// Note: X-UI API for updating clients can be inconsistent. This implementation assumes the /panel/inbound/updateClient/{clientId} endpoint
// exists and works by passing the full client settings structure again.
func (c *Client) UpdateClient(ctx context.Context, inboundID int, clientUUID string, settings ClientSettings) error {
	// Ensure login before operation
	if err := c.ensureLogin(ctx); err != nil {
		return err
	}

	// Prepare the API endpoint URL - обновлено для 3x-ui API
	endpoint := path.Join(c.apiPath, "updateClient", clientUUID)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	// Create the request body with the full settings structure
	reqBody := map[string]interface{}{
		"id":         inboundID, // The inbound ID
		"enable":     settings.Enable,
		"email":      settings.Email,
		"expiryTime": settings.ExpiryTime,
		"totalGB":    settings.TotalGB,
		// Include other relevant fields from ClientSettings if needed by the API
		"limitIp": settings.LimitIPs,
		"subId":   settings.SubscriptionID,
		"tgId":    settings.TelegramID,
	}

	// Execute the request
	resp, err := c.executeRequest(ctx, http.MethodPost, apiURL.String(), reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Parse the response
	var response APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		c.logger.ErrorContext(ctx, "Failed to decode client update response", slog.String("error", err.Error()))
		return fmt.Errorf("failed to decode API response: %w", err)
	}

	// Check the response status
	if !response.Success {
		c.logger.ErrorContext(ctx, "X-UI failed to update client",
			slog.Int("inbound_id", inboundID),
			slog.String("client_uuid", clientUUID),
			slog.String("message", response.Message),
		)
		return fmt.Errorf("x-ui API error during update: %s", response.Message)
	}

	c.logger.InfoContext(ctx, "Successfully updated x-ui client",
		slog.Int("inbound_id", inboundID),
		slog.String("client_uuid", clientUUID),
		slog.Bool("enabled", settings.Enable),
	)
	return nil
}

// DeleteClient removes a client from a specific inbound.
// clientId is the identifier used by x-ui (UUID for VLESS/VMess, email for SS, password for Trojan).
func (c *Client) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	// URL encode the clientID in case it contains special characters
	encodedClientID := url.PathEscape(clientID)
	endpoint := path.Join(c.apiPath, strconv.Itoa(inboundID), "delClient", encodedClientID)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil) // No request body for delete
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return fmt.Errorf("failed to decode delete client response: %w", err)
	}

	if !genericResp.Success {
		// Check if the message indicates the client was already gone
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") || strings.Contains(strings.ToLower(genericResp.Msg), "does not exist") {
			c.logger.Warn("Attempted to delete non-existent x-ui client", slog.Int("inbound_id", inboundID), slog.String("client_id", clientID), slog.String("msg", genericResp.Msg))
			return ErrClientNotFound // Return a specific error
		}
		c.logger.Error("Failed to delete x-ui client", slog.Int("inbound_id", inboundID), slog.String("client_id", clientID), slog.String("msg", genericResp.Msg))
		return fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	c.logger.Info("Successfully deleted x-ui client", slog.Int("inbound_id", inboundID), slog.String("client_id", clientID))
	return nil
}

// GetClientSettings получает настройки клиента из inbound по его email или id
func (c *Client) GetClientSettings(ctx context.Context, inboundID int, clientIDorEmail string) (*ClientSettings, error) {
	c.logger.InfoContext(ctx, "Получение настроек клиента",
		slog.Int("inbound_id", inboundID),
		slog.String("client_id", clientIDorEmail))

	// Получаем настройки инбаунда
	inbound, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения inbound: %w", err)
	}

	// Перебираем клиентов в настройках и ищем с нужным ID или email
	for _, client := range inbound.Clients {
		// Проверяем совпадение по email
		if client.Email == clientIDorEmail {
			c.logger.InfoContext(ctx, "Найден клиент по email",
				slog.String("email", client.Email),
				slog.String("uuid", client.UUID))
			return &client, nil
		}

		// Проверяем совпадение по ID (UUID)
		// В зависимости от протокола, ID может быть в разных полях
		switch strings.ToLower(inbound.Protocol) {
		case "vless", "vmess":
			if client.UUID == clientIDorEmail {
				c.logger.InfoContext(ctx, "Найден клиент по UUID",
					slog.String("uuid", client.UUID),
					slog.String("email", client.Email))
				return &client, nil
			}
		case "trojan", "shadowsocks":
			if client.Password == clientIDorEmail {
				c.logger.InfoContext(ctx, "Найден клиент по Password",
					slog.String("password", client.Password),
					slog.String("email", client.Email))
				return &client, nil
			}
		}
	}

	c.logger.WarnContext(ctx, "Клиент не найден в inbound",
		slog.Int("inbound_id", inboundID),
		slog.String("client_id", clientIDorEmail),
		slog.Int("client_count", len(inbound.Clients)))

	return nil, fmt.Errorf("клиент %s не найден в inbound %d", clientIDorEmail, inboundID)
}

// GetClientsByMetadata ищет клиентов в инбаундах с учетом метаданных
func (c *Client) GetClientsByMetadata(ctx context.Context, tgID string, subID string) (*ClientSettings, int, error) {
	c.logger.InfoContext(ctx, "Поиск клиентов по метаданным",
		slog.String("tg_id", tgID),
		slog.String("sub_id", subID))

	// Получаем все inbound'ы
	inbounds, err := c.GetAllInbounds(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка получения inbounds: %w", err)
	}

	// Ищем клиента с совпадающими metadata во всех инбаундах
	for _, inbound := range inbounds {
		settings := ""
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			c.logger.WarnContext(ctx, "Не удалось распарсить настройки инбаунда",
				slog.Int("inbound_id", inbound.ID),
				slog.Any("error", err))
			continue
		}

		// Поиск клиентов по различным протоколам
		switch inbound.Protocol {
		case "vless", "vmess", "trojan", "shadowsocks":
			// Проверяем всех клиентов в инбаунде
			var clientEmail string
			var clientFound bool

			// Пытаемся найти клиента, у которого tgId и subId совпадают с искомыми
			if strings.Contains(settings, fmt.Sprintf(`"tgId":"%s"`, tgID)) &&
				strings.Contains(settings, fmt.Sprintf(`"subId":"%s"`, subID)) {
				c.logger.InfoContext(ctx, "Найдены совпадения в метаданных клиента",
					slog.Int("inbound_id", inbound.ID))

				// Получаем клиентов этого инбаунда для более точного поиска
				if inbound.ClientStats != nil {
					for _, client := range inbound.ClientStats {
						if client.TgID == tgID && client.SubID == subID {
							clientEmail = client.Email
							clientFound = true
							c.logger.InfoContext(ctx, "Найден клиент по tgId и subId",
								slog.Int("inbound_id", inbound.ID),
								slog.String("email", clientEmail))
							break
						}
					}
				}

				// Если нашли клиента, получаем его полные настройки
				if clientFound {
					settings, err := c.GetClientSettings(ctx, inbound.ID, clientEmail)
					if err == nil && settings != nil {
						return settings, inbound.ID, nil
					}
				}
			}
		}
	}

	return nil, 0, ErrClientNotFound
}

// UpdateClientMetadata обновляет метаданные клиента (TelegramID и SubscriptionID)
func (c *Client) UpdateClientMetadata(ctx context.Context, email string, tgID string, subID string) error {
	c.logger.InfoContext(ctx, "Обновление метаданных клиента",
		slog.String("email", email),
		slog.String("tg_id", tgID),
		slog.String("sub_id", subID))

	// Получаем все inbound'ы для поиска клиента
	inbounds, err := c.GetAllInbounds(ctx)
	if err != nil {
		return fmt.Errorf("ошибка получения inbounds: %w", err)
	}

	// Ищем клиента и его inbound
	var foundInboundID int
	var clientSettings *ClientSettings

	for _, inbound := range inbounds {
		// Проверяем наличие клиента
		settings, err := c.GetClientSettings(ctx, inbound.ID, email)
		if err == nil && settings != nil {
			foundInboundID = inbound.ID
			clientSettings = settings
			c.logger.InfoContext(ctx, "Найден клиент для обновления",
				slog.String("email", email),
				slog.Int("inbound_id", inbound.ID))
			break
		}
	}

	if foundInboundID == 0 || clientSettings == nil {
		return fmt.Errorf("клиент %s не найден ни в одном inbound", email)
	}

	// Обновляем метаданные
	clientSettings.TelegramID = tgID
	clientSettings.SubscriptionID = subID

	// Обновляем клиента в системе
	err = c.UpdateClient(ctx, foundInboundID, clientSettings.UUID, *clientSettings)
	if err != nil {
		c.logger.ErrorContext(ctx, "Ошибка обновления метаданных клиента",
			slog.String("email", email),
			slog.String("tg_id", tgID),
			slog.String("sub_id", subID),
			slog.Any("error", err))
		return fmt.Errorf("ошибка обновления клиента: %w", err)
	}

	c.logger.InfoContext(ctx, "Метаданные клиента успешно обновлены",
		slog.String("email", email),
		slog.String("tg_id", tgID),
		slog.String("sub_id", subID))
	return nil
}

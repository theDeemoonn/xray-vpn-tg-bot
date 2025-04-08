package xui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"golang.org/x/net/publicsuffix"
)

var (
	ErrLoginFailed     = errors.New("xui: login failed")
	ErrRequestFailed   = errors.New("xui: request failed")
	ErrClientNotFound  = errors.New("xui: client not found")
	ErrInboundNotFound = errors.New("xui: inbound not found")
	ErrOperationFailed = errors.New("xui: operation failed") // Generic failure
)

// Client interacts with the 3x-ui panel API.
// API documentation: https://github.com/MHSanaei/3x-ui#api-routes
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	apiPath    string // Base path for API endpoints, e.g., "/panel/api/inbounds"
	username   string
	password   string
	logger     *slog.Logger
}

// NewClient creates a new x-ui API client.
func NewClient(baseURL string, username string, password string, apiTimeout time.Duration, logger *slog.Logger) (*Client, error) {
	parsedURL, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	httpClient := &http.Client{
		Jar:     jar,
		Timeout: apiTimeout,
		// Consider adding transport settings (TLS config, proxy, etc.) if needed
	}

	return &Client{
		httpClient: httpClient,
		baseURL:    parsedURL,
		apiPath:    "/panel/api/inbounds", // Обновленный API путь для 3x-ui
		username:   username,
		password:   password,
		logger:     logger.With(slog.String("component", "xui_client"), slog.String("base_url", parsedURL.String())),
	}, nil
}

// Login authenticates the client with the x-ui panel.
// It's automatically called by other methods if needed, but can be called explicitly.
func (c *Client) Login(ctx context.Context) error {
	loginURL := c.baseURL.ResolveReference(&url.URL{Path: "/login"})
	reqBody := LoginRequest{
		Username: c.username,
		Password: c.password,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("Attempting x-ui login", slog.String("url", loginURL.String()))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.logger.Error("x-ui login failed", slog.Int("status_code", resp.StatusCode), slog.String("body", string(bodyBytes)))
		return fmt.Errorf("%w: unexpected status code %d", ErrLoginFailed, resp.StatusCode)
	}

	// Check cookies - x-ui login sets a session cookie
	if len(c.httpClient.Jar.Cookies(loginURL)) == 0 {
		c.logger.Error("x-ui login succeeded (status 200) but no session cookie was set")
		return fmt.Errorf("%w: no session cookie received", ErrLoginFailed)
	}

	c.logger.Info("x-ui login successful")
	return nil
}

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

// GetClientTraffic retrieves traffic details for a specific client email.
// NOTE: This might not be the best way to get a config link.
// Explore if x-ui has a direct endpoint for generating links/QR codes.
func (c *Client) GetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	encodedEmail := url.PathEscape(email)
	endpoint := path.Join(c.apiPath, "getClientTraffics", encodedEmail)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode get client traffic response: %w", err)
	}

	if !genericResp.Success {
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			c.logger.Warn("Client traffic not found", slog.String("email", email), slog.String("msg", genericResp.Msg))
			return nil, ErrClientNotFound
		}
		c.logger.Error("Failed to get client traffic", slog.String("email", email), slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	if genericResp.Obj == nil {
		c.logger.Warn("Client traffic not found (obj is nil)", slog.String("email", email), slog.String("msg", genericResp.Msg))
		return nil, ErrClientNotFound
	}

	// The actual client traffic data is inside the Obj field.
	// We need to marshal Obj back to JSON and then unmarshal into ClientTraffic.
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client traffic obj: %w", err)
	}

	var clientTraffic ClientTraffic
	if err := json.Unmarshal(objBytes, &clientTraffic); err != nil {
		return nil, fmt.Errorf("failed to unmarshal client traffic obj: %w", err)
	}

	return &clientTraffic, nil
}

// GetAllInbounds retrieves all inbounds from the 3x-ui panel.
func (c *Client) GetAllInbounds(ctx context.Context) ([]InboundRaw, error) {
	endpoint := path.Join(c.apiPath, "list")
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode get all inbounds response: %w", err)
	}

	if !genericResp.Success {
		c.logger.Error("Failed to get all inbounds", slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	if genericResp.Obj == nil {
		return []InboundRaw{}, nil // Возвращаем пустой массив, если нет данных
	}

	// Преобразуем Obj в массив InboundRaw
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inbounds obj: %w", err)
	}

	var inbounds []InboundRaw
	if err := json.Unmarshal(objBytes, &inbounds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inbounds obj: %w", err)
	}

	return inbounds, nil
}

// GetOnlineUsers retrieves a list of emails of currently online users.
func (c *Client) GetOnlineUsers(ctx context.Context) ([]string, error) {
	endpoint := path.Join(c.apiPath, "onlines")
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode online users response: %w", err)
	}

	if !genericResp.Success {
		c.logger.Error("Failed to get online users", slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	if genericResp.Obj == nil {
		return []string{}, nil // Возвращаем пустой массив, если нет онлайн пользователей
	}

	// Преобразуем Obj в массив строк (emails)
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal online users obj: %w", err)
	}

	var emails []string
	if err := json.Unmarshal(objBytes, &emails); err != nil {
		return nil, fmt.Errorf("failed to unmarshal online users obj: %w", err)
	}

	return emails, nil
}

// ResetClientTraffic сбрасывает статистику трафика для клиента по email
func (c *Client) ResetClientTraffic(ctx context.Context, inboundID int, email string) error {
	encodedEmail := url.PathEscape(email)
	endpoint := path.Join(c.apiPath, strconv.Itoa(inboundID), "resetClientTraffic", encodedEmail)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return fmt.Errorf("failed to decode reset client traffic response: %w", err)
	}

	if !genericResp.Success {
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			c.logger.Warn("Client not found for traffic reset", slog.Int("inbound_id", inboundID), slog.String("email", email), slog.String("msg", genericResp.Msg))
			return ErrClientNotFound
		}
		c.logger.Error("Failed to reset client traffic", slog.Int("inbound_id", inboundID), slog.String("email", email), slog.String("msg", genericResp.Msg))
		return fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	c.logger.Info("Successfully reset client traffic", slog.Int("inbound_id", inboundID), slog.String("email", email))
	return nil
}

// ResetAllTraffics сбрасывает трафик для всех inbounds
func (c *Client) ResetAllTraffics(ctx context.Context) error {
	endpoint := path.Join(c.apiPath, "resetAllTraffics")
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return fmt.Errorf("failed to decode reset all traffics response: %w", err)
	}

	if !genericResp.Success {
		c.logger.Error("Failed to reset all traffics", slog.String("msg", genericResp.Msg))
		return fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	c.logger.Info("Successfully reset all traffics")
	return nil
}

// GetClientIps получает список IP-адресов клиента по email
func (c *Client) GetClientIps(ctx context.Context, email string) ([]string, error) {
	encodedEmail := url.PathEscape(email)
	endpoint := path.Join(c.apiPath, "clientIps", encodedEmail)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode client IPs response: %w", err)
	}

	if !genericResp.Success {
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			c.logger.Warn("Client not found for IPs lookup", slog.String("email", email), slog.String("msg", genericResp.Msg))
			return nil, ErrClientNotFound
		}
		c.logger.Error("Failed to get client IPs", slog.String("email", email), slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	if genericResp.Obj == nil {
		return []string{}, nil // Пустой список IP-адресов
	}

	// Преобразуем Obj в массив строк (IP адресов)
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client IPs obj: %w", err)
	}

	var ips []string
	if err := json.Unmarshal(objBytes, &ips); err != nil {
		return nil, fmt.Errorf("failed to unmarshal client IPs obj: %w", err)
	}

	return ips, nil
}

// ClearClientIps очищает список IP-адресов клиента по email
func (c *Client) ClearClientIps(ctx context.Context, email string) error {
	encodedEmail := url.PathEscape(email)
	endpoint := path.Join(c.apiPath, "clearClientIps", encodedEmail)
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return fmt.Errorf("failed to decode clear client IPs response: %w", err)
	}

	if !genericResp.Success {
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			c.logger.Warn("Client not found for clearing IPs", slog.String("email", email), slog.String("msg", genericResp.Msg))
			return ErrClientNotFound
		}
		c.logger.Error("Failed to clear client IPs", slog.String("email", email), slog.String("msg", genericResp.Msg))
		return fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	c.logger.Info("Successfully cleared client IPs", slog.String("email", email))
	return nil
}

// GetInbound fetches and parses an inbound with all its settings.
func (c *Client) GetInbound(ctx context.Context, inboundID int) (*InboundSettings, error) {
	endpoint := path.Join(c.apiPath, "get", strconv.Itoa(inboundID))
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err // Error from doRequestWithLogin
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode get inbound response: %w", err)
	}

	if !genericResp.Success {
		if genericResp.Msg == "The inbound is not found." || strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			return nil, ErrInboundNotFound
		}
		c.logger.Error("Failed to get inbound details", slog.Int("inbound_id", inboundID), slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}
	if genericResp.Obj == nil {
		return nil, ErrInboundNotFound // Or a different error? Inbound exists but obj is nil
	}

	// Marshal Obj back to JSON and unmarshal into InboundRaw
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inbound obj: %w", err)
	}

	var rawInbound InboundRaw
	if err := json.Unmarshal(objBytes, &rawInbound); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inbound obj: %w", err)
	}

	// Parse settings and streamSettings into native Go structures
	inboundSettings, err := c.parseInboundRaw(&rawInbound)
	if err != nil {
		return nil, fmt.Errorf("failed to parse inbound settings: %w", err)
	}

	// Now parse clients based on protocol
	var clients []ClientSettings
	switch rawInbound.Protocol {
	case "vless":
		var vlessSettings VlessSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &vlessSettings); err != nil {
			c.logger.Error("Failed to unmarshal VLESS settings", slog.Any("error", err))
			return inboundSettings, nil // Continue with parsed inboundSettings without clients
		}

		for _, vlessClient := range vlessSettings.Clients {
			clients = append(clients, ClientSettings{
				Email:  vlessClient.Email,
				UUID:   vlessClient.ID,
				Flow:   vlessClient.Flow,
				Enable: true, // Assuming all clients in settings are enabled
			})
		}

	case "vmess":
		var vmessSettings VMessSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &vmessSettings); err != nil {
			c.logger.Error("Failed to unmarshal VMess settings", slog.Any("error", err))
			return inboundSettings, nil
		}

		for _, vmessClient := range vmessSettings.Clients {
			clients = append(clients, ClientSettings{
				Email:  vmessClient.Email,
				UUID:   vmessClient.ID,
				Enable: true,
			})
		}

	case "trojan":
		var trojanSettings TrojanSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &trojanSettings); err != nil {
			c.logger.Error("Failed to unmarshal Trojan settings", slog.Any("error", err))
			return inboundSettings, nil
		}

		for _, trojanClient := range trojanSettings.Clients {
			clients = append(clients, ClientSettings{
				Email:    trojanClient.Email,
				Password: trojanClient.Password,
				Flow:     trojanClient.Flow,
				Enable:   true,
			})
		}

	case "shadowsocks":
		var ssSettings ShadowsocksSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &ssSettings); err != nil {
			c.logger.Error("Failed to unmarshal Shadowsocks settings", slog.Any("error", err))
			return inboundSettings, nil
		}

		for _, ssClient := range ssSettings.Clients {
			method := ssClient.Method
			if method == "" {
				method = ssSettings.Method
			}

			clients = append(clients, ClientSettings{
				Email:    ssClient.Email,
				Password: ssClient.Password,
				Method:   method,
				Enable:   true,
			})
		}
	}

	// Add parsed clients to inboundSettings
	inboundSettings.Clients = clients

	c.logger.InfoContext(ctx, "Successfully parsed inbound with clients",
		slog.Int("inbound_id", inboundID),
		slog.String("protocol", rawInbound.Protocol),
		slog.Int("client_count", len(clients)))

	return inboundSettings, nil
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

// GetClientQRCode получает QR-код для клиента напрямую из 3x-ui
func (c *Client) GetClientQRCode(ctx context.Context, email string, protocol string) ([]byte, error) {
	c.logger.InfoContext(ctx, "Генерация QR-кода",
		slog.String("email", email),
		slog.String("protocol", protocol))

	// Попробуем сначала получить QR код напрямую через API 3x-ui
	if qrBytes, err := c.getQRCodeFromAPI(ctx, email); err == nil {
		c.logger.InfoContext(ctx, "Успешно получен QR-код через API 3x-ui",
			slog.String("email", email),
			slog.Int("qr_size_bytes", len(qrBytes)))
		return qrBytes, nil
	} else {
		c.logger.InfoContext(ctx, "Не удалось получить QR-код через API, генерируем локально",
			slog.String("email", email),
			slog.Any("error", err))
	}

	// Если не удалось получить QR через API, генерируем локально
	c.logger.InfoContext(ctx, "Локальная генерация QR-кода", slog.String("email", email))

	// Получаем конфигурационную ссылку для этого клиента
	configLink, err := c.GetClientConfigLink(ctx, email, protocol)
	if err != nil {
		c.logger.ErrorContext(ctx, "Не удалось получить ссылку конфигурации для QR-кода",
			slog.String("email", email),
			slog.String("protocol", protocol),
			slog.Any("error", err))
		return nil, fmt.Errorf("не удалось получить ссылку конфигурации для QR-кода: %w", err)
	}

	c.logger.InfoContext(ctx, "Успешно получена ссылка для QR-кода",
		slog.String("email", email),
		slog.String("config_link", configLink))

	// Генерируем QR-код из ссылки с помощью библиотеки qrcode
	qrCode, err := qrcode.Encode(configLink, qrcode.Medium, 256)
	if err != nil {
		c.logger.ErrorContext(ctx, "Ошибка генерации QR-кода",
			slog.String("email", email),
			slog.String("config_link", configLink),
			slog.Any("error", err))
		return nil, fmt.Errorf("ошибка генерации QR-кода: %w", err)
	}

	c.logger.InfoContext(ctx, "QR-код успешно сгенерирован локально",
		slog.String("email", email),
		slog.Int("qr_size_bytes", len(qrCode)))
	return qrCode, nil
}

// getQRCodeFromAPI пытается получить QR-код напрямую через API 3x-ui
func (c *Client) getQRCodeFromAPI(ctx context.Context, email string) ([]byte, error) {
	// Проверяем авторизацию
	if err := c.ensureLogin(ctx); err != nil {
		return nil, err
	}

	// Получаем все inbound'ы для поиска клиента
	inbounds, err := c.GetAllInbounds(ctx)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения inbounds: %w", err)
	}

	// Ищем клиента и его inbound
	var foundInboundID int
	for _, inbound := range inbounds {
		// Проверяем, содержит ли inbound клиента с нужным email
		if strings.Contains(inbound.Settings, fmt.Sprintf(`"email":"%s"`, email)) {
			foundInboundID = inbound.ID
			c.logger.InfoContext(ctx, "Найден клиент в inbound",
				slog.String("email", email),
				slog.Int("inbound_id", inbound.ID))
			break
		}
	}

	if foundInboundID == 0 {
		c.logger.WarnContext(ctx, "Не удалось найти клиента по email в inbounds",
			slog.String("email", email))
		return nil, fmt.Errorf("клиент с email %s не найден в inbounds", email)
	}

	// Пробуем разные варианты URL для получения QR-кода
	// 1. Вариант для 3x-ui
	qrURL := c.baseURL.ResolveReference(&url.URL{
		Path: fmt.Sprintf("/panel/inbound/%d/getClientQrcode/%s", foundInboundID, url.PathEscape(email)),
	})

	c.logger.InfoContext(ctx, "Запрос QR-кода через API (вариант 1)",
		slog.String("url", qrURL.String()),
		slog.String("email", email))

	// Создаем запрос с контекстом и cookies
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qrURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.WarnContext(ctx, "Ошибка запроса QR-кода (вариант 1)",
			slog.String("url", qrURL.String()),
			slog.Any("error", err))

		// Пробуем второй вариант URL (x-ui)
		qrURL2 := c.baseURL.ResolveReference(&url.URL{
			Path: fmt.Sprintf("/xui/inbound/%d/getClientQrcode/%s", foundInboundID, url.PathEscape(email)),
		})

		c.logger.InfoContext(ctx, "Запрос QR-кода через API (вариант 2)",
			slog.String("url", qrURL2.String()),
			slog.String("email", email))

		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, qrURL2.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("ошибка создания запроса: %w", err)
		}

		resp2, err := c.httpClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("ошибка выполнения запроса (оба варианта): %w", err)
		}
		resp = resp2
	}
	defer resp.Body.Close()

	// Проверяем код ответа
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("сервер вернул код %d при запросе QR-кода: %s",
			resp.StatusCode, string(bodyBytes))
		c.logger.WarnContext(ctx, errMsg,
			slog.String("email", email),
			slog.String("content_type", resp.Header.Get("Content-Type")))
		return nil, fmt.Errorf(errMsg)
	}

	// Проверяем тип содержимого ответа
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		// Если это не изображение, возможно это JSON с ошибкой
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.logger.WarnContext(ctx, "Сервер вернул неожиданный тип контента",
			slog.String("email", email),
			slog.String("content_type", contentType),
			slog.String("body", string(bodyBytes)))
		return nil, fmt.Errorf("сервер вернул неожиданный тип контента: %s, тело: %s", contentType, string(bodyBytes))
	}

	// Читаем изображение QR-кода из ответа
	qrBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения данных QR-кода: %w", err)
	}

	if len(qrBytes) == 0 {
		return nil, fmt.Errorf("получены пустые данные QR-кода")
	}

	c.logger.InfoContext(ctx, "Успешно получен QR-код",
		slog.String("email", email),
		slog.Int("size_bytes", len(qrBytes)),
		slog.String("content_type", contentType))
	return qrBytes, nil
}

// GetClientConfigLink генерирует конфигурационную ссылку для клиента
func (c *Client) GetClientConfigLink(ctx context.Context, clientID string, protocol string) (string, error) {
	// Получаем все inbound'ы
	inbounds, err := c.GetAllInbounds(ctx)
	if err != nil {
		return "", fmt.Errorf("ошибка получения inbounds: %w", err)
	}

	// Ищем клиента в inbound'ах
	var inboundID int
	var clientSettings *ClientSettings

	for _, inbound := range inbounds {
		// Проверяем наличие клиента
		settings, err := c.GetClientSettings(ctx, inbound.ID, clientID)
		if err == nil && settings != nil {
			inboundID = inbound.ID
			clientSettings = settings
			break
		}
	}

	if inboundID == 0 || clientSettings == nil {
		return "", fmt.Errorf("клиент %s не найден ни в одном inbound", clientID)
	}

	// Получаем настройки inbound
	inboundSettings, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return "", fmt.Errorf("ошибка получения настроек inbound: %w", err)
	}

	// Для формирования правильной ссылки используем публичный хост сервера
	// Берем только имя хоста без протокола
	hostname := c.baseURL.Hostname() // Например, 38.244.152.237

	// Избавляемся от любых протоколов в hostname, если они по какой-то причине остались
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimPrefix(hostname, "https://")

	// Убираем порты из имени хоста, если они есть
	if idx := strings.Index(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}

	// Используем порт из inbound настроек, а не порт API
	port := inboundSettings.Port // Порт из inbound настроек - например, 43455

	// Проверяем протокол
	inboundProtocol := strings.ToLower(inboundSettings.Protocol)
	requestedProtocol := strings.ToLower(protocol)

	if requestedProtocol != "" && requestedProtocol != inboundProtocol {
		c.logger.WarnContext(ctx, "Запрошенный протокол не соответствует протоколу inbound",
			slog.String("requested", requestedProtocol),
			slog.String("actual", inboundProtocol))
	}

	var configLink string

	switch inboundProtocol {
	case "vless":
		// Формат: vless://UUID@HOSTNAME:PORT?type=NETWORK&security=SECURITY...#REMARK
		u := url.URL{
			Scheme: "vless",
			User:   url.User(clientSettings.UUID),
			Host:   fmt.Sprintf("%s:%d", hostname, port),
		}

		q := u.Query()
		q.Set("type", inboundSettings.Network)

		// Настройки сети
		switch inboundSettings.Network {
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
		}

		// Настройки безопасности
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

		if clientSettings.Flow != "" {
			q.Set("flow", clientSettings.Flow)
		}

		u.RawQuery = q.Encode()

		// Используем понятное имя для пользователя
		remark := clientSettings.Email
		if clientSettings.TelegramID != "" && clientSettings.SubscriptionID != "" {
			remark = fmt.Sprintf("user_%s_%s", clientSettings.TelegramID, clientSettings.SubscriptionID)
		}

		u.Fragment = remark

		configLink = u.String()

	case "vmess":
		// Для VMess используем формат конфигурации JSON, закодированный в base64
		vmessConfig := map[string]interface{}{
			"v":    "2",
			"ps":   clientSettings.Email,
			"add":  hostname,
			"port": port,
			"id":   clientSettings.UUID,
			"aid":  0, // AlterId обычно 0 в новых версиях
			"net":  inboundSettings.Network,
			"type": "none",
		}

		// Настройки безопасности
		if inboundSettings.Security != "none" {
			vmessConfig["tls"] = inboundSettings.Security
		}

		// Настройки для разных типов сетей
		switch inboundSettings.Network {
		case "ws":
			vmessConfig["path"] = inboundSettings.WSPath
			if inboundSettings.WSHost != "" {
				vmessConfig["host"] = inboundSettings.WSHost
			}
		case "grpc":
			vmessConfig["path"] = inboundSettings.GRPCService
			vmessConfig["type"] = "gun"
		}

		// Кодируем в JSON
		configJSON, err := json.Marshal(vmessConfig)
		if err != nil {
			return "", fmt.Errorf("ошибка маршалинга VMess конфигурации: %w", err)
		}

		// Кодируем в base64
		configBase64 := base64.StdEncoding.EncodeToString(configJSON)
		configLink = "vmess://" + configBase64

	case "trojan":
		// Формат: trojan://PASSWORD@HOSTNAME:PORT?security=SECURITY...#REMARK
		password := clientSettings.Password
		if password == "" {
			password = clientSettings.UUID // Иногда UUID используется как пароль
		}

		u := url.URL{
			Scheme: "trojan",
			User:   url.User(password),
			Host:   fmt.Sprintf("%s:%d", hostname, port),
		}

		q := u.Query()

		// Настройки безопасности
		if inboundSettings.Security != "none" {
			q.Set("security", inboundSettings.Security)
		}

		if inboundSettings.SNI != "" {
			q.Set("sni", inboundSettings.SNI)
		}

		// Настройки сети
		if inboundSettings.Network != "tcp" {
			q.Set("type", inboundSettings.Network)

			switch inboundSettings.Network {
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
			}
		}

		u.RawQuery = q.Encode()

		// Используем понятное имя для пользователя
		remark := clientSettings.Email
		if clientSettings.TelegramID != "" && clientSettings.SubscriptionID != "" {
			remark = fmt.Sprintf("user_%s_%s", clientSettings.TelegramID, clientSettings.SubscriptionID)
		}

		u.Fragment = remark

		configLink = u.String()

	case "shadowsocks":
		// Формат: ss://BASE64(METHOD:PASSWORD)@HOSTNAME:PORT#REMARK
		method := clientSettings.Method
		if method == "" {
			method = "aes-256-gcm" // Дефолтный метод, если не указан
		}

		password := clientSettings.Password
		if password == "" {
			password = clientSettings.UUID
		}

		// Кодируем метод и пароль
		methodPass := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))

		u := url.URL{
			Scheme: "ss",
			User:   url.User(methodPass),
			Host:   fmt.Sprintf("%s:%d", hostname, port),
		}

		// Используем понятное имя для пользователя
		remark := clientSettings.Email
		if clientSettings.TelegramID != "" && clientSettings.SubscriptionID != "" {
			remark = fmt.Sprintf("user_%s_%s", clientSettings.TelegramID, clientSettings.SubscriptionID)
		}

		u.Fragment = remark

		configLink = u.String()

	default:
		return "", fmt.Errorf("неподдерживаемый протокол для генерации конфигурации: %s", inboundProtocol)
	}

	return configLink, nil
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

// parseInboundRaw парсит сырые данные inbound в структурированный формат InboundSettings
func (c *Client) parseInboundRaw(rawInbound *InboundRaw) (*InboundSettings, error) {
	// Разбираем streamSettings
	var streamSettings StreamSettings
	if err := json.Unmarshal([]byte(rawInbound.StreamSettings), &streamSettings); err != nil {
		c.logger.Error("Failed to unmarshal inbound streamSettings JSON",
			slog.Int("inbound_id", rawInbound.ID),
			slog.String("json_string", rawInbound.StreamSettings),
			slog.Any("error", err))
		// Продолжаем без stream settings, заполним что можно
	}

	// Собираем результирующую структуру
	settings := &InboundSettings{
		ID:       rawInbound.ID,
		Remark:   rawInbound.Remark,
		Port:     rawInbound.Port,
		Protocol: rawInbound.Protocol,
		Network:  streamSettings.Network,
		Security: streamSettings.Security,
	}

	// Заполняем поля TLS настроек, если они есть
	if streamSettings.TLSSettings != nil {
		settings.SNI = streamSettings.TLSSettings.ServerName
		settings.Fingerprint = streamSettings.TLSSettings.Fingerprint
	}

	// Заполняем поля REALITY настроек, если они есть
	if streamSettings.RealitySettings != nil {
		settings.Security = "reality" // Явно устанавливаем security если есть reality settings
		settings.PublicKey = streamSettings.RealitySettings.PublicKey
		settings.ShortID = streamSettings.RealitySettings.ShortID
		settings.SpiderX = streamSettings.RealitySettings.SpiderX
		// Используем первый ServerName как SNI для REALITY, если TLS SNI не задан
		if settings.SNI == "" && len(streamSettings.RealitySettings.ServerNames) > 0 {
			settings.SNI = streamSettings.RealitySettings.ServerNames[0]
		}
	}

	// Заполняем поля WebSocket настроек, если они есть
	if streamSettings.WSSettings != nil {
		settings.WSPath = streamSettings.WSSettings.Path
		if streamSettings.WSSettings.Headers != nil {
			settings.WSHost = streamSettings.WSSettings.Headers["Host"] // Распространенная практика
		}
	}

	// Заполняем поля gRPC настроек, если они есть
	if streamSettings.GrpcSettings != nil {
		settings.GRPCService = streamSettings.GrpcSettings.ServiceName
	}

	// Добавляем статистику клиентов, если они есть
	settings.ClientStats = rawInbound.ClientStats

	c.logger.Info("Successfully parsed inbound settings",
		slog.Int("inbound_id", settings.ID),
		slog.String("remark", settings.Remark))

	return settings, nil
}

// --- Helper Methods ---

// ensureLogin checks if a login is needed and performs it.
func (c *Client) ensureLogin(ctx context.Context) error {
	baseURL, _ := url.Parse(c.baseURL.String()) // Use base URL for cookie check
	if len(c.httpClient.Jar.Cookies(baseURL)) == 0 {
		c.logger.Info("No session cookie found, attempting login.")
		if err := c.Login(ctx); err != nil {
			return err // Login failed
		}
	}
	return nil
}

// executeRequest performs a generic HTTP request with context and body, ensuring login and handling re-login.
func (c *Client) executeRequest(ctx context.Context, method, urlStr string, reqBody interface{}) (*http.Response, error) {
	if err := c.ensureLogin(ctx); err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if reqBody != nil {
		jsonBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.logger.Debug("Sending x-ui API request", slog.String("method", method), slog.String("url", urlStr))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestFailed, err)
	}

	// Check for non-OK status codes immediately
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Attempt to re-login on 401 Unauthorized
		if resp.StatusCode == http.StatusUnauthorized {
			c.logger.Warn("Received 401 Unauthorized, attempting re-login and retry")
			_ = resp.Body.Close()

			if loginErr := c.Login(ctx); loginErr != nil {
				return nil, loginErr
			}

			// Recreate request reader if it was consumed
			var bodyReaderRetry io.Reader
			if reqBody != nil {
				jsonBytesRetry, _ := json.Marshal(reqBody) // Error handled above
				bodyReaderRetry = bytes.NewBuffer(jsonBytesRetry)
			}

			reqRetry, errRetry := http.NewRequestWithContext(ctx, method, urlStr, bodyReaderRetry)
			if errRetry != nil {
				return nil, fmt.Errorf("failed to create retry request: %w", errRetry)
			}
			reqRetry.Header.Set("Accept", "application/json")
			if bodyReaderRetry != nil {
				reqRetry.Header.Set("Content-Type", "application/json")
			}

			c.logger.Debug("Retrying x-ui API request after re-login", slog.String("method", method), slog.String("url", urlStr))
			respRetry, errRetry := c.httpClient.Do(reqRetry)
			if errRetry != nil {
				return nil, fmt.Errorf("%w: retry failed: %w", ErrRequestFailed, errRetry)
			}
			// Check status code of the retry attempt
			if respRetry.StatusCode < 200 || respRetry.StatusCode >= 300 {
				bodyBytes, _ := io.ReadAll(respRetry.Body)
				_ = respRetry.Body.Close()
				c.logger.Error("x-ui API request failed on retry", slog.String("method", method), slog.String("url", urlStr), slog.Int("status_code", respRetry.StatusCode), slog.String("body", string(bodyBytes)))
				return nil, fmt.Errorf("%w: retry failed with status code %d", ErrRequestFailed, respRetry.StatusCode)
			}
			return respRetry, nil // Return successful retry response
		}

		// Handle other non-OK statuses
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		c.logger.Error("x-ui API request failed", slog.String("method", method), slog.String("url", urlStr), slog.Int("status_code", resp.StatusCode), slog.String("body", string(bodyBytes)))
		return nil, fmt.Errorf("%w: unexpected status code %d", ErrRequestFailed, resp.StatusCode)
	}

	return resp, nil
}

// doRequestWithLogin performs an HTTP request, attempting to login first if necessary.
func (c *Client) doRequestWithLogin(ctx context.Context, method, urlStr string, body io.Reader) (*http.Response, error) {
	// Check if we likely have a valid session cookie
	baseURL, _ := url.Parse(c.baseURL.String()) // Use base URL for cookie check
	if len(c.httpClient.Jar.Cookies(baseURL)) == 0 {
		c.logger.Info("No session cookie found, attempting login first.")
		if err := c.Login(ctx); err != nil {
			return nil, err // Login failed
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.logger.Debug("Sending x-ui API request", slog.String("method", method), slog.String("url", urlStr))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestFailed, err)
	}

	// If unauthorized (e.g., cookie expired), try logging in again and retrying the request once.
	if resp.StatusCode == http.StatusUnauthorized {
		c.logger.Warn("Received 401 Unauthorized, attempting re-login and retry")
		_ = resp.Body.Close() // Close the previous response body

		if loginErr := c.Login(ctx); loginErr != nil {
			return nil, loginErr // Re-login failed
		}

		// Recreate request with potentially fresh body if it was consumed
		// For simplicity, assuming body (if present) is repeatable or nil
		// If body is not repeatable (like a live stream), this needs adjustment.
		reqRetry, errRetry := http.NewRequestWithContext(ctx, method, urlStr, body)
		if errRetry != nil {
			return nil, fmt.Errorf("failed to create retry request: %w", errRetry)
		}
		reqRetry.Header.Set("Accept", "application/json")
		if body != nil {
			reqRetry.Header.Set("Content-Type", "application/json")
		}

		c.logger.Debug("Retrying x-ui API request after re-login", slog.String("method", method), slog.String("url", urlStr))
		respRetry, errRetry := c.httpClient.Do(reqRetry)
		if errRetry != nil {
			return nil, fmt.Errorf("%w: %w", ErrRequestFailed, errRetry)
		}
		// Return the response from the retry attempt
		return respRetry, nil
	}

	// Check for other non-OK statuses that aren't handled specifically
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // Close body after reading

		// Для статусов 500 пытаемся прочитать и логировать детали ошибки
		if resp.StatusCode == http.StatusInternalServerError && len(bodyBytes) > 0 {
			var errorResp map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &errorResp); err == nil {
				// Если удалось разобрать JSON, логируем структурированную ошибку
				c.logger.Error("Server returned error 500",
					slog.String("method", method),
					slog.String("url", urlStr),
					slog.Any("error_details", errorResp))
			} else {
				// Если не удалось разобрать JSON, логируем как строку
				c.logger.Error("Server returned error 500",
					slog.String("method", method),
					slog.String("url", urlStr),
					slog.String("error_body", string(bodyBytes)))
			}
		} else {
			c.logger.Error("x-ui API request failed",
				slog.String("method", method),
				slog.String("url", urlStr),
				slog.Int("status_code", resp.StatusCode),
				slog.String("body", string(bodyBytes)))
		}

		return nil, fmt.Errorf("%w: unexpected status code %d", ErrRequestFailed, resp.StatusCode)
	}

	return resp, nil
}

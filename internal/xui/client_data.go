package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

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

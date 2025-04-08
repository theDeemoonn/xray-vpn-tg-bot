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

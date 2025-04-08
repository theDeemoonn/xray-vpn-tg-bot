package xui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/skip2/go-qrcode"
)

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

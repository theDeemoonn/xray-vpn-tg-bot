package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

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

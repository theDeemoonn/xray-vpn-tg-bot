package yookassa

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
	"time"

	"github.com/google/uuid"
)

const (
	defaultAPIURL        = "https://api.yookassa.ru/v3"
	defaultTimeout       = 10 * time.Second
	idempotencyKeyHeader = "Idempotence-Key"
)

var (
	ErrRequestFailed    = errors.New("yookassa: request failed")
	ErrUnexpectedStatus = errors.New("yookassa: unexpected status code")
	ErrAPIError         = errors.New("yookassa: api error")
)

// Client interacts with the YooKassa API v3.
type Client struct {
	httpClient *http.Client
	apiURL     string
	shopID     string
	secretKey  string
	logger     *slog.Logger
}

// NewClient creates a new YooKassa API client.
func NewClient(shopID, secretKey string, logger *slog.Logger, options ...ClientOption) (*Client, error) {
	if shopID == "" {
		return nil, errors.New("yookassa: shopID is required")
	}
	if secretKey == "" {
		return nil, errors.New("yookassa: secretKey is required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil)) // Default to discard logger
	}

	c := &Client{
		apiURL:    defaultAPIURL,
		shopID:    shopID,
		secretKey: secretKey,
		logger:    logger.With(slog.String("component", "yookassa_client"), slog.String("shop_id", shopID)),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}

	// Apply options
	for _, opt := range options {
		opt(c)
	}

	return c, nil
}

// ClientOption allows configuring the YooKassa client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithAPIURL sets a custom API endpoint URL.
func WithAPIURL(apiURL string) ClientOption {
	return func(c *Client) {
		if apiURL != "" {
			c.apiURL = apiURL
		}
	}
}

// WithTimeout sets a custom timeout for HTTP requests.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// CreatePayment sends a request to create a new payment.
// It automatically generates an Idempotence-Key.
func (c *Client) CreatePayment(ctx context.Context, req CreatePaymentRequest) (*PaymentResponse, error) {
	endpoint := c.apiURL + "/payments"
	idempotencyKey := uuid.New().String()

	var resp PaymentResponse
	err := c.doRequest(ctx, http.MethodPost, endpoint, idempotencyKey, req, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetPayment retrieves details about an existing payment.
func (c *Client) GetPayment(ctx context.Context, paymentID string) (*PaymentResponse, error) {
	if paymentID == "" {
		return nil, errors.New("yookassa: paymentID is required for GetPayment")
	}
	endpoint := fmt.Sprintf("%s/payments/%s", c.apiURL, paymentID)

	var resp PaymentResponse
	err := c.doRequest(ctx, http.MethodGet, endpoint, "", nil, &resp) // No body or idempotency key for GET
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// doRequest handles making the HTTP request to the YooKassa API.
func (c *Client) doRequest(ctx context.Context, method, url, idempotencyKey string, reqBody, respBody interface{}) error {
	var bodyReader io.Reader
	if reqBody != nil {
		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("yookassa: failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBody)
		c.logger.Debug("Request body", slog.String("body", string(jsonBody)))
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("yookassa: failed to create http request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Set Idempotency Key if provided
	if idempotencyKey != "" {
		httpReq.Header.Set(idempotencyKeyHeader, idempotencyKey)
	}

	// Set Basic Authentication
	auth := base64.StdEncoding.EncodeToString([]byte(c.shopID + ":" + c.secretKey))
	httpReq.Header.Set("Authorization", "Basic "+auth)

	c.logger.Debug("Sending request to YooKassa API",
		slog.String("method", method),
		slog.String("url", url),
		slog.String(idempotencyKeyHeader, idempotencyKey),
	)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("yookassa: failed to read response body: %w", err)
	}
	c.logger.Debug("Received response", slog.Int("status_code", resp.StatusCode), slog.String("body", string(respBytes)))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Attempt to decode as an API error
		var apiErr ErrorResponse
		if decodeErr := json.Unmarshal(respBytes, &apiErr); decodeErr == nil && apiErr.Type == "error" {
			c.logger.Error("YooKassa API error",
				slog.Int("status_code", resp.StatusCode),
				slog.String("error_code", apiErr.Code),
				slog.String("description", apiErr.Description),
				slog.String("parameter", apiErr.Parameter),
				slog.String("request_id", apiErr.ID),
			)
			// You might want to wrap the specific API error details
			return fmt.Errorf("%w: %s: %s (parameter: %s)", ErrAPIError, apiErr.Code, apiErr.Description, apiErr.Parameter)
		}
		// Otherwise, return a generic status error
		return fmt.Errorf("%w: %d - %s", ErrUnexpectedStatus, resp.StatusCode, string(respBytes))
	}

	if respBody != nil {
		if err := json.Unmarshal(respBytes, respBody); err != nil {
			return fmt.Errorf("yookassa: failed to unmarshal successful response body: %w", err)
		}
	}

	return nil
}

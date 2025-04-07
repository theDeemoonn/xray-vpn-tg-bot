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
	"path"
	"strconv"
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

// Client interacts with the x-ui panel API.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	apiPath    string // Base path for API endpoints, e.g., "/xui/API/"
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
		apiPath:    "/xui/API/inbounds", // Default API path
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

	// x-ui expects client settings as a JSON string within the main JSON body
	clientSettingsJSON, err := json.Marshal([]ClientSettings{client})
	if err != nil {
		return fmt.Errorf("failed to marshal client settings: %w", err)
	}

	reqPayload := map[string]interface{}{
		"id":       inboundID,
		"settings": string(clientSettingsJSON),
	}

	jsonBody, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal add client request: %w", err)
	}

	resp, err := c.doRequestWithLogin(ctx, http.MethodPost, apiURL.String(), bytes.NewBuffer(jsonBody))
	if err != nil {
		return err // Error already wrapped in doRequestWithLogin
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return fmt.Errorf("failed to decode add client response: %w", err)
	}

	if !genericResp.Success {
		c.logger.Error("Failed to add x-ui client", slog.Int("inbound_id", inboundID), slog.String("client_email", client.Email), slog.String("msg", genericResp.Msg))
		return fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	c.logger.Info("Successfully added x-ui client", slog.Int("inbound_id", inboundID), slog.String("client_email", client.Email))
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

// GetInbound retrieves and parses settings for a specific inbound ID.
func (c *Client) GetInbound(ctx context.Context, inboundID int) (*InboundSettings, error) {
	endpoint := path.Join(c.apiPath, "get", strconv.Itoa(inboundID))
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode get inbound response: %w", err)
	}

	if !genericResp.Success {
		if strings.Contains(strings.ToLower(genericResp.Msg), "not found") {
			c.logger.Warn("Inbound not found", slog.Int("inbound_id", inboundID), slog.String("msg", genericResp.Msg))
			return nil, ErrInboundNotFound
		}
		c.logger.Error("Failed to get inbound details", slog.Int("inbound_id", inboundID), slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}

	if genericResp.Obj == nil {
		c.logger.Warn("Inbound not found (obj is nil)", slog.Int("inbound_id", inboundID), slog.String("msg", genericResp.Msg))
		return nil, ErrInboundNotFound
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

	// Now parse the nested JSON strings
	var streamSettings StreamSettings
	if err := json.Unmarshal([]byte(rawInbound.StreamSettings), &streamSettings); err != nil {
		c.logger.Error("Failed to unmarshal inbound streamSettings JSON", slog.Int("inbound_id", inboundID), slog.String("json_string", rawInbound.StreamSettings), slog.Any("error", err))
		// Continue without stream settings? Or return error?
		// return nil, fmt.Errorf("failed to parse stream settings: %w", err)
	}

	// Parse protocol-specific settings (example for VLESS)
	// TODO: Handle other protocols (VMess, Trojan, etc.)
	if rawInbound.Protocol == "vless" {
		var vlessSettings VlessSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &vlessSettings); err != nil {
			c.logger.Error("Failed to unmarshal inbound vless settings JSON", slog.Int("inbound_id", inboundID), slog.String("json_string", rawInbound.Settings), slog.Any("error", err))
			// Continue without client settings? Or return error?
		}
	}

	// Assemble the final InboundSettings struct
	settings := &InboundSettings{
		ID:       rawInbound.ID,
		Remark:   rawInbound.Remark,
		Port:     rawInbound.Port,
		Protocol: rawInbound.Protocol,
		Network:  streamSettings.Network,
		Security: streamSettings.Security,
	}

	if streamSettings.TLSSettings != nil {
		settings.SNI = streamSettings.TLSSettings.ServerName
		settings.Fingerprint = streamSettings.TLSSettings.Fingerprint
	}
	if streamSettings.RealitySettings != nil {
		settings.Security = "reality" // Explicitly set security if reality settings exist
		settings.PublicKey = streamSettings.RealitySettings.PublicKey
		settings.ShortID = streamSettings.RealitySettings.ShortID
		settings.SpiderX = streamSettings.RealitySettings.SpiderX
		// Use first server name as SNI for REALITY if TLS SNI is not set
		if settings.SNI == "" && len(streamSettings.RealitySettings.ServerNames) > 0 {
			settings.SNI = streamSettings.RealitySettings.ServerNames[0]
		}
	}
	if streamSettings.WSSettings != nil {
		settings.WSPath = streamSettings.WSSettings.Path
		if streamSettings.WSSettings.Headers != nil {
			settings.WSHost = streamSettings.WSSettings.Headers["Host"] // Common practice
		}
	}
	if streamSettings.GrpcSettings != nil {
		settings.GRPCService = streamSettings.GrpcSettings.ServiceName
	}

	c.logger.Info("Successfully retrieved and parsed inbound settings", slog.Int("inbound_id", inboundID), slog.String("remark", settings.Remark))
	return settings, nil
}

// GetClientSettings retrieves the specific settings for a client within an inbound.
// It fetches the inbound details and parses the client list.
func (c *Client) GetClientSettings(ctx context.Context, inboundID int, clientEmail string) (*ClientSettings, error) {
	endpoint := path.Join(c.apiPath, "get", strconv.Itoa(inboundID))
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})

	resp, err := c.doRequestWithLogin(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err // Error from doRequestWithLogin
	}
	defer resp.Body.Close()

	var genericResp GenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to decode get inbound response for client settings: %w", err)
	}

	if !genericResp.Success {
		// Don't return ErrInboundNotFound here, let the main error handle it
		c.logger.Error("Failed to get inbound details while fetching client settings", slog.Int("inbound_id", inboundID), slog.String("msg", genericResp.Msg))
		return nil, fmt.Errorf("%w: %s", ErrOperationFailed, genericResp.Msg)
	}
	if genericResp.Obj == nil {
		return nil, ErrInboundNotFound // Or a different error? Inbound exists but obj is nil
	}

	// Marshal Obj back to JSON and unmarshal into InboundRaw
	objBytes, err := json.Marshal(genericResp.Obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inbound obj for client settings: %w", err)
	}

	var rawInbound InboundRaw
	if err := json.Unmarshal(objBytes, &rawInbound); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inbound obj for client settings: %w", err)
	}

	// Parse the settings JSON based on protocol
	// TODO: Handle other protocols
	if rawInbound.Protocol == "vless" {
		var vlessSettings VlessSettings
		if err := json.Unmarshal([]byte(rawInbound.Settings), &vlessSettings); err != nil {
			c.logger.Error("Failed to unmarshal inbound vless settings JSON for client lookup", slog.Int("inbound_id", inboundID), slog.Any("error", err))
			return nil, fmt.Errorf("failed to parse inbound settings: %w", err)
		}

		// Find the client by email
		for _, client := range vlessSettings.Clients {
			if client.Email == clientEmail {
				c.logger.Info("Found client settings by email", slog.Int("inbound_id", inboundID), slog.String("email", clientEmail))
				// Map VlessClientSetting to the more generic ClientSettings
				// Note: This assumes ClientSettings has the necessary fields (like UUID/ID, Flow)
				// We used 'id' in VlessClientSetting which corresponds to UUID
				foundClient := &ClientSettings{
					Email: client.Email,
					UUID:  client.ID, // Map ID to UUID
					Flow:  client.Flow,
					// Enable, TotalGB, ExpiryTime are often managed separately or via GetClientTraffic
					// We might need to merge data if necessary, but for config link, UUID/Flow are key.
					Enable: true, // Assume enabled if found? Or get from GetClientTraffic?
				}
				return foundClient, nil
			}
		}
		// Client not found in the list
		c.logger.Warn("Client email not found in inbound settings", slog.Int("inbound_id", inboundID), slog.String("email", clientEmail))
		return nil, ErrClientNotFound

	} else {
		// Handle other protocols (VMess, Trojan, etc.) here
		c.logger.Error("GetClientSettings not implemented for protocol", slog.String("protocol", rawInbound.Protocol))
		return nil, fmt.Errorf("getting client settings for protocol '%s' not implemented", rawInbound.Protocol)
	}
}

// TODO: Implement functions to generate config links (vmess://, vless://, etc.) based on Inbound and Client settings.

// --- Helper Methods ---

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
		c.logger.Error("x-ui API request failed", slog.String("method", method), slog.String("url", urlStr), slog.Int("status_code", resp.StatusCode), slog.String("body", string(bodyBytes)))
		return nil, fmt.Errorf("%w: unexpected status code %d", ErrRequestFailed, resp.StatusCode)
	}

	return resp, nil
}

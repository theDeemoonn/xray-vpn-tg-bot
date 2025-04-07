package xui

// LoginRequest represents the request body for x-ui login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// GenericResponse represents a common response structure from x-ui.
type GenericResponse struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     any    `json:"obj"` // Can be different types depending on the endpoint
}

// --- Add Client Models ---

// AddClientRequest represents the data needed to add a new client.
// Fields are based on typical x-ui client settings. Adjust as needed.
type AddClientRequest struct {
	ID      int              `json:"id"`       // Inbound ID
	Clients []ClientSettings `json:"settings"` // Note: x-ui API expects {"id": ..., "settings": "[{\"email\": ...}]"}
}

// ClientSettings represents the settings for a single client.
// Fields depend on the inbound protocol (VMess, VLESS, Trojan, SS).
// Using a generic struct, specific fields might be needed for different protocols.
type ClientSettings struct {
	Email          string `json:"email"`                // Unique identifier for the client
	UUID           string `json:"id,omitempty"`         // VMess/VLESS UUID (use 'id' field in JSON)
	Password       string `json:"password,omitempty"`   // Trojan/Shadowsocks password
	Flow           string `json:"flow,omitempty"`       // VLESS flow (e.g., "xtls-rprx-vision")
	TotalGB        int    `json:"totalGB,omitempty"`    // Traffic limit in GB (0 for unlimited)
	ExpiryTime     int64  `json:"expiryTime,omitempty"` // Expiry timestamp (milliseconds since epoch, 0 for unlimited)
	Enable         bool   `json:"enable"`               // Client enabled status
	TelegramID     string `json:"tgId,omitempty"`       // Optional Telegram ID
	SubscriptionID string `json:"subId,omitempty"`      // Optional Subscription ID
	LimitIP        int    `json:"limitIp,omitempty"`    // Optional IP limit
}

// AddClientResponse represents the object returned on successful client addition.
type AddClientResponse struct {
	// X-UI doesn't seem to return specific client info on add,
	// relies on GenericResponse.Success
}

// --- Get Inbound Models ---

// Inbound represents the structure of an inbound configuration.
// Only include fields relevant for generating client configs/links.
type Inbound struct {
	ID             int    `json:"id"`
	UserID         int    `json:"userId"`
	Up             int64  `json:"up"`
	Down           int64  `json:"down"`
	Total          int64  `json:"total"` // Total traffic limit in bytes (0 for unlimited)
	Remark         string `json:"remark"`
	Enable         bool   `json:"enable"`
	ExpiryTime     int64  `json:"expiryTime"` // Expiry timestamp (milliseconds)
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`       // e.g., "vless", "vmess", "trojan"
	Settings       string `json:"settings"`       // JSON string of inbound settings
	StreamSettings string `json:"streamSettings"` // JSON string of stream settings
	Tag            string `json:"tag"`
	Sniffing       string `json:"sniffing"` // JSON string of sniffing settings

	// Derived fields (populated after fetching)
	ClientSettings []ClientSettings `json:"-"` // Parsed client settings (if applicable, might need separate parsing)
}

// InboundRaw represents the raw structure returned by x-ui API for an inbound.
// Fields like settings and streamSettings are typically JSON strings.
type InboundRaw struct {
	ID             int    `json:"id"`
	UserID         int    `json:"userId"`
	Up             int64  `json:"up"`
	Down           int64  `json:"down"`
	Total          int64  `json:"total"` // Total traffic limit in bytes (0 for unlimited)
	Remark         string `json:"remark"`
	Enable         bool   `json:"enable"`
	ExpiryTime     int64  `json:"expiryTime"` // Expiry timestamp (milliseconds)
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`       // e.g., "vless", "vmess", "trojan"
	Settings       string `json:"settings"`       // JSON string of inbound settings
	StreamSettings string `json:"streamSettings"` // JSON string of stream settings
	Tag            string `json:"tag"`
	Sniffing       string `json:"sniffing"` // JSON string of sniffing settings
}

// VlessClientSetting represents a client within VLESS settings JSON.
// Needed because settings JSON contains a list of clients.
type VlessClientSetting struct {
	ID    string `json:"id"`             // UUID
	Flow  string `json:"flow,omitempty"` // Optional flow
	Email string `json:"email"`          // Email used as identifier
	// Add other fields if necessary (level, etc.)
}

// VlessSettings represents the parsed JSON from InboundRaw.Settings for VLESS protocol.
type VlessSettings struct {
	Clients    []VlessClientSetting `json:"clients"`
	Decryption string               `json:"decryption"` // Usually "none"
	Fallbacks  []any                `json:"fallbacks,omitempty"`
}

// WSSettings represents WebSocket settings within StreamSettings.
type WSSettings struct {
	AcceptProxyProtocol bool              `json:"acceptProxyProtocol,omitempty"`
	Path                string            `json:"path,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
}

// GrpcSettings represents gRPC settings within StreamSettings.
type GrpcSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
}

// TLSSettings represents TLS settings within StreamSettings.
type TLSSettings struct {
	ServerName       string `json:"serverName,omitempty"` // SNI
	RejectUnknownSni bool   `json:"rejectUnknownSni,omitempty"`
	// MinVersion     string `json:"minVersion,omitempty"`
	// MaxVersion     string `json:"maxVersion,omitempty"`
	// CipherSuites string `json:"cipherSuites,omitempty"`
	Certificates []struct {
		CertificateFile string `json:"certificateFile,omitempty"`
		KeyFile         string `json:"keyFile,omitempty"`
	} `json:"certificates,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

// RealitySettings represents REALITY settings within StreamSettings.
type RealitySettings struct {
	Show        bool     `json:"show,omitempty"`
	Dest        string   `json:"dest"` // Required: Target server address:port
	Xver        int      `json:"xver,omitempty"`
	ServerNames []string `json:"serverNames"` // Required: SNI list
	PrivateKey  string   `json:"privateKey"`  // Required: Private key
	PublicKey   string   `json:"publicKey,omitempty"`
	// MinClientVer string   `json:"minClientVer,omitempty"`
	// MaxClientVer string   `json:"maxClientVer,omitempty"`
	// MaxTimeDiff  int64    `json:"maxTimeDiff,omitempty"`
	ShortID string `json:"shortId"` // Required: Short ID list (comma separated?)
	SpiderX string `json:"spiderX,omitempty"`
}

// StreamSettings represents the parsed JSON from InboundRaw.StreamSettings.
type StreamSettings struct {
	Network         string           `json:"network"`  // tcp, kcp, ws, http, quic, grpc
	Security        string           `json:"security"` // none, tls, reality
	TLSSettings     *TLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *RealitySettings `json:"realitySettings,omitempty"`
	WSSettings      *WSSettings      `json:"wsSettings,omitempty"`
	GrpcSettings    *GrpcSettings    `json:"grpcSettings,omitempty"`
	// Add other network types (kcpSettings, httpSettings, quicSettings, etc.) if needed
}

// InboundSettings represents the combined and parsed settings for an inbound.
// This is the structure our service layer will use.
type InboundSettings struct {
	ID          int
	Remark      string
	Port        int
	Protocol    string // vless, vmess, trojan etc.
	Network     string // tcp, ws, grpc etc.
	Security    string // none, tls, reality
	SNI         string // From tlsSettings.ServerName or realitySettings.ServerNames[0]
	Fingerprint string // From tlsSettings.Fingerprint
	PublicKey   string // From realitySettings.PublicKey
	ShortID     string // From realitySettings.ShortID
	SpiderX     string // From realitySettings.SpiderX
	WSPath      string // From wsSettings.Path
	WSHost      string // From wsSettings.Headers["Host"]
	GRPCService string // From grpcSettings.ServiceName
	// Add other relevant fields derived from settings
}

// ClientTraffic represents the traffic stats for a specific client (email).
type ClientTraffic struct {
	ID         int    `json:"id"`
	InboundID  int    `json:"inboundId"`
	Enable     bool   `json:"enable"`
	Email      string `json:"email"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"` // Traffic limit in bytes
}

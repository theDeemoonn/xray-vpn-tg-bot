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

// APIResponse is a more specific structure for responses like addClient, updateClient
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"msg"` // Use 'msg' field as per observed API behavior
	Obj     any    `json:"obj,omitempty"`
}

// --- Add Client Models ---

// AddClientRequest represents the data needed to add a new client.
// Fields are based on typical x-ui client settings. Adjust as needed.
type AddClientRequest struct {
	ID      int              `json:"id"`       // Inbound ID
	Clients []ClientSettings `json:"settings"` // Note: x-ui API expects {"id": ..., "settings": "[{\"email\": ...}]"}
}

// ClientSettings represents the settings for a client in an inbound.
// Based on common fields used in addClient/updateClient.
type ClientSettings struct {
	Email          string `json:"email"`
	UUID           string `json:"id"`                   // Mapped to UUID for VMESS/VLESS
	Password       string `json:"password,omitempty"`   // For Trojan
	Method         string `json:"method,omitempty"`     // For Shadowsocks
	Flow           string `json:"flow,omitempty"`       // Flow control (e.g., "xtls-rprx-vision")
	Enable         bool   `json:"enable"`               // Enable/disable client
	ExpiryTime     int64  `json:"expiryTime,omitempty"` // Expiration timestamp (milliseconds)
	TotalBytes     int64  `json:"totalGB,omitempty"`    // Traffic limit in Bytes (0 = unlimited). API expects key "totalGB".
	LimitIPs       int    `json:"limitIp,omitempty"`    // Max IPs allowed (0 = unlimited)
	SubscriptionID string `json:"subId,omitempty"`      // Optional Subscription ID
	TelegramID     string `json:"tgId,omitempty"`       // Optional Telegram ID (as string)
	// WireGuard specific fields
	PrivateKey    string `json:"privateKey,omitempty"`    // For WireGuard
	PublicKey     string `json:"publicKey,omitempty"`     // For WireGuard
	PreSharedKey  string `json:"presharedKey,omitempty"`  // For WireGuard
	AllowedIPs    string `json:"allowedIPs,omitempty"`    // For WireGuard, comma-separated
	ClientAddress string `json:"clientAddress,omitempty"` // For WireGuard
	// Дополнительное поле для передачи протокола
	Protocol string `json:"-"` // Не отправляем в JSON, используем внутри
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
	ID             int          `json:"id"`
	UserID         int          `json:"userId"`
	Up             int64        `json:"up"`
	Down           int64        `json:"down"`
	Total          int64        `json:"total"` // Total traffic limit in bytes (0 for unlimited)
	Remark         string       `json:"remark"`
	Enable         bool         `json:"enable"`
	ExpiryTime     int64        `json:"expiryTime"` // Expiry timestamp (milliseconds)
	Listen         string       `json:"listen"`
	Port           int          `json:"port"`
	Protocol       string       `json:"protocol"`       // e.g., "vless", "vmess", "trojan"
	Settings       string       `json:"settings"`       // JSON string of inbound settings
	StreamSettings string       `json:"streamSettings"` // JSON string of stream settings
	Tag            string       `json:"tag"`
	Sniffing       string       `json:"sniffing"`              // JSON string of sniffing settings
	ClientStats    []ClientInfo `json:"clientStats,omitempty"` // Used in some responses
}

// ClientInfo represents basic client info sometimes included in InboundRaw
type ClientInfo struct {
	ID         interface{} `json:"id"` // UUID или числовой ID
	Flow       string      `json:"flow"`
	Email      string      `json:"email"`
	Total      int         `json:"totalGB"`
	ExpiryTime int64       `json:"expiryTime"`
	Enable     bool        `json:"enable"`
	LimitIP    int         `json:"limitIp"` // Number of IPs allowed
	TgID       string      `json:"tgId"`
	SubID      string      `json:"subId"`
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
	Fallbacks  []interface{}        `json:"fallbacks"`  // Type depends on actual usage
}

// VMessSettings представляет собой настройки протокола VMess
type VMessSettings struct {
	Clients    []VMESSClientSetting `json:"clients"`
	Decryption string               `json:"decryption,omitempty"` // Обычно "none"
}

// VMESSClientSetting представляет настройки клиента VMess
type VMESSClientSetting struct {
	ID       string `json:"id"`    // UUID
	Email    string `json:"email"` // Идентификатор электронной почты
	AlterId  int    `json:"alterId,omitempty"`
	Security string `json:"security,omitempty"` // Метод шифрования
}

// TrojanSettings представляет собой настройки протокола Trojan
type TrojanSettings struct {
	Clients   []TrojanClientSetting `json:"clients"`
	Fallbacks []interface{}         `json:"fallbacks,omitempty"`
}

// TrojanClientSetting представляет настройки клиента Trojan
type TrojanClientSetting struct {
	Password string `json:"password"` // Пароль вместо UUID
	Email    string `json:"email"`    // Идентификатор электронной почты
	Flow     string `json:"flow,omitempty"`
}

// ShadowsocksSettings представляет собой настройки протокола Shadowsocks
type ShadowsocksSettings struct {
	Clients  []ShadowsocksClientSetting `json:"clients"`
	Network  string                     `json:"network,omitempty"`
	Method   string                     `json:"method"` // Метод шифрования
	Password string                     `json:"password,omitempty"`
}

// ShadowsocksClientSetting представляет настройки клиента Shadowsocks
type ShadowsocksClientSetting struct {
	Email    string `json:"email"`            // Email используется в качестве идентификатора
	Password string `json:"password"`         // Пароль
	Method   string `json:"method,omitempty"` // Метод шифрования, если отличается от общего
}

// WireGuardSettings представляет собой настройки протокола WireGuard
type WireGuardSettings struct {
	Clients      []WireGuardClientSetting `json:"clients"`
	LocalAddress []string                 `json:"localAddress"` // Локальные адреса сервера
	PrivateKey   string                   `json:"privateKey"`   // Приватный ключ сервера
	Mtu          int                      `json:"mtu,omitempty"`
}

// WireGuardClientSetting представляет настройки клиента WireGuard
type WireGuardClientSetting struct {
	Email         string   `json:"email"`                  // Идентификатор электронной почты
	PublicKey     string   `json:"publicKey"`              // Публичный ключ клиента
	PrivateKey    string   `json:"privateKey,omitempty"`   // Приватный ключ, если генерируется сервером
	PreSharedKey  string   `json:"preSharedKey,omitempty"` // Общий предварительный ключ
	AllowedIPs    []string `json:"allowedIPs"`             // Разрешенные IP адреса
	ClientAddress []string `json:"clientAddress"`          // Адреса клиента
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
	// Add ClientStats if needed for UpdateClient
	ClientStats []ClientInfo     `json:"-"` // Exclude from standard JSON, populate manually if needed
	Clients     []ClientSettings `json:"-"` // Список клиентов, связанных с этим inbound
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

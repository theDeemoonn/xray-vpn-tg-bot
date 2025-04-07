package yookassa

import "time"

// Currency represents the ISO 4217 currency code.
type Currency string

const (
	CurrencyRUB Currency = "RUB"
	// Add other currencies if needed
)

// PaymentStatus represents the status of a payment.
type PaymentStatus string

const (
	PaymentStatusPending           PaymentStatus = "pending"             // Ожидает оплаты
	PaymentStatusWaitingForCapture PaymentStatus = "waiting_for_capture" // Ожидает подтверждения (успешный холд)
	PaymentStatusSucceeded         PaymentStatus = "succeeded"           // Успешно оплачен
	PaymentStatusCanceled          PaymentStatus = "canceled"            // Отменен
)

// ConfirmationType represents the type of payment confirmation.
type ConfirmationType string

const (
	ConfirmationTypeRedirect ConfirmationType = "redirect"
	// Add other types if needed (e.g., embedded, external)
)

// ReceiptRegistrationStatus represents the status of receipt registration.
type ReceiptRegistrationStatus string

const (
	ReceiptRegistrationPending   ReceiptRegistrationStatus = "pending"
	ReceiptRegistrationSucceeded ReceiptRegistrationStatus = "succeeded"
	ReceiptRegistrationCanceled  ReceiptRegistrationStatus = "canceled"
)

// Amount represents a monetary value.
type Amount struct {
	Value    string   `json:"value"`              // Monetary value (e.g., "100.00")
	Currency Currency `json:"currency,omitempty"` // Currency code
}

// Confirmation represents the instructions for the user to complete payment.
type Confirmation struct {
	Type            ConfirmationType `json:"type"`                 // Confirmation type (e.g., "redirect")
	ConfirmationURL string           `json:"confirmation_url"`     // URL for redirection
	ReturnURL       string           `json:"return_url,omitempty"` // URL to redirect after successful/failed payment
	Enforce         bool             `json:"enforce,omitempty"`    // Force confirmation process
	Locale          string           `json:"locale,omitempty"`     // Language for payment interface (e.g., "ru_RU")
}

// Recipient represents the recipient of the payment (usually the shop).
type Recipient struct {
	AccountID string `json:"account_id"` // Your shop ID in YooKassa
	GatewayID string `json:"gateway_id"` // Payment gateway ID (often same as AccountID)
}

// PaymentMethodData represents data specific to a payment method.
// This is a placeholder; specific methods (bank_card, etc.) have different fields.
type PaymentMethodData struct {
	Type string `json:"type"` // e.g., "bank_card"
	// Add fields specific to payment methods if needed
}

// Payer represents the customer making the payment.
type Payer struct {
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
	// Other fields like name, etc., might be applicable
}

// VatDataType represents the type of VAT calculation.
type VatDataType string

const (
	VatDataTypeUntaxed    VatDataType = "untaxed"    // без НДС
	VatDataTypeCalculated VatDataType = "calculated" // НДС по ставке X/1XX (например, 10/110, 20/120).
	VatDataTypeMixed      VatDataType = "mixed"      // НДС по ставке X% (например, 10%, 20%).
)

// VatCode represents the tax rate code.
type VatCode int

const (
	VatCodeUntaxed VatCode = 1 // без НДС
	VatCode0       VatCode = 2 // НДС 0%
	VatCode10      VatCode = 3 // НДС 10%
	VatCode20      VatCode = 4 // НДС 20%
	VatCode10_110  VatCode = 5 // НДС 10/110
	VatCode20_120  VatCode = 6 // НДС 20/120
)

// PaymentMode represents the payment mode attribute for receipts (ФФД 1.05).
type PaymentMode string

// PaymentSubject represents the payment subject attribute for receipts (ФФД 1.05).
type PaymentSubject string

// Customer represents the customer details for the receipt.
type Customer struct {
	FullName string `json:"full_name,omitempty"` // ФИО
	Inn      string `json:"inn,omitempty"`       // ИНН
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
}

// ReceiptItem represents an item in the receipt.
type ReceiptItem struct {
	Description    string         `json:"description"`               // Item name
	Quantity       string         `json:"quantity"`                  // Quantity (e.g., "1.0")
	Amount         Amount         `json:"amount"`                    // Total price for this quantity
	VatCode        VatCode        `json:"vat_code"`                  // VAT rate code
	PaymentMode    PaymentMode    `json:"payment_mode,omitempty"`    // Признак способа расчета (ФФД 1.05+)
	PaymentSubject PaymentSubject `json:"payment_subject,omitempty"` // Признак предмета расчета (ФФД 1.05+)
	// Add other fields like excise, country_code, etc. if needed
}

// Settlement represents a settlement transaction in the receipt.
type Settlement struct {
	Type   string `json:"type"`   // e.g., "prepayment", "advance"
	Amount Amount `json:"amount"` // Settlement amount
}

// Receipt represents the fiscal receipt data (54-ФЗ).
type Receipt struct {
	Customer      Customer      `json:"customer,omitempty"`
	Items         []ReceiptItem `json:"items"`
	TaxSystemCode int           `json:"tax_system_code,omitempty"` // Tax system code
	Phone         string        `json:"phone,omitempty"`           // For sending the receipt
	Email         string        `json:"email,omitempty"`           // For sending the receipt
	Settlements   []Settlement  `json:"settlements,omitempty"`
}

// CreatePaymentRequest represents the request body for creating a payment.
type CreatePaymentRequest struct {
	Amount             Amount         `json:"amount"`                         // Payment amount
	Description        string         `json:"description,omitempty"`          // Payment description for the user
	Receipt            *Receipt       `json:"receipt,omitempty"`              // Receipt details (for 54-FZ)
	Recipient          *Recipient     `json:"recipient,omitempty"`            // Recipient info (usually not needed if authenticated via API key)
	PaymentMethodID    string         `json:"payment_method_id,omitempty"`    // ID of saved payment method
	PaymentMethodData  map[string]any `json:"payment_method_data,omitempty"`  // Data for specific payment method (e.g., card details)
	Confirmation       *Confirmation  `json:"confirmation,omitempty"`         // Confirmation instructions
	SavePaymentMethod  bool           `json:"save_payment_method,omitempty"`  // Save payment method for future use
	Capture            bool           `json:"capture,omitempty"`              // Auto-capture payment (true by default)
	ClientIP           string         `json:"client_ip,omitempty"`            // User's IP address
	Metadata           map[string]any `json:"metadata,omitempty"`             // Custom metadata
	MerchantCustomerID string         `json:"merchant_customer_id,omitempty"` // Your internal customer ID
}

// PaymentResponse represents the response received after creating or retrieving a payment.
type PaymentResponse struct {
	ID                   string                    `json:"id"`                              // YooKassa Payment ID
	Status               PaymentStatus             `json:"status"`                          // Payment status
	Amount               Amount                    `json:"amount"`                          // Payment amount
	IncomeAmount         *Amount                   `json:"income_amount,omitempty"`         // Amount received by shop after commission
	Description          string                    `json:"description,omitempty"`           // Payment description
	Recipient            Recipient                 `json:"recipient"`                       // Recipient info
	PaymentMethod        PaymentMethodData         `json:"payment_method,omitempty"`        // Payment method used
	CapturedAt           *time.Time                `json:"captured_at,omitempty"`           // Time of capture
	CreatedAt            time.Time                 `json:"created_at"`                      // Time of creation
	ExpiresAt            *time.Time                `json:"expires_at,omitempty"`            // Expiration time (for pending payments)
	Confirmation         *Confirmation             `json:"confirmation,omitempty"`          // Confirmation details (if status is pending)
	Test                 bool                      `json:"test"`                            // Is it a test payment?
	Paid                 bool                      `json:"paid"`                            // Is the payment considered paid?
	Refundable           bool                      `json:"refundable"`                      // Can the payment be refunded?
	ReceiptRegistration  ReceiptRegistrationStatus `json:"receipt_registration,omitempty"`  // Receipt status
	Metadata             map[string]any            `json:"metadata,omitempty"`              // Custom metadata
	CancellationDetails  *CancellationDetails      `json:"cancellation_details,omitempty"`  // Details if canceled
	AuthorizationDetails *AuthorizationDetails     `json:"authorization_details,omitempty"` // Authorization details (e.g., RRN for cards)
}

// CancellationDetails provides information about why a payment was cancelled.
type CancellationDetails struct {
	Party  string `json:"party"`  // Initiator (e.g., "yoo_kassa", "merchant", "payment_network")
	Reason string `json:"reason"` // Reason code (e.g., "expired_on_confirmation", "canceled_by_merchant")
}

// AuthorizationDetails provides details about payment authorization.
type AuthorizationDetails struct {
	Rrn          string        `json:"rrn,omitempty"`       // Retrieval Reference Number (bank card payments)
	AuthCode     string        `json:"auth_code,omitempty"` // Authorization Code (bank card payments)
	ThreeDSecure *ThreeDSecure `json:"three_d_secure,omitempty"`
}

// ThreeDSecure contains 3-D Secure authentication results.
type ThreeDSecure struct {
	Applied bool `json:"applied"` // Was 3-D Secure applied?
}

// --- Webhook Notification ---

// NotificationType represents the type of event in a webhook notification.
type NotificationType string

const (
	NotificationTypePaymentSucceeded NotificationType = "payment.succeeded"
	NotificationTypePaymentWaiting   NotificationType = "payment.waiting_for_capture"
	NotificationTypePaymentCanceled  NotificationType = "payment.canceled"
	NotificationTypeRefundSucceeded  NotificationType = "refund.succeeded"
	// Add other notification types if needed
)

// NotificationEvent represents the event type in a webhook notification.
type NotificationEvent string

const (
	EventPaymentSucceeded         NotificationEvent = "payment.succeeded"
	EventPaymentWaitingForCapture NotificationEvent = "payment.waiting_for_capture"
	EventPaymentCanceled          NotificationEvent = "payment.canceled"
	EventRefundSucceeded          NotificationEvent = "refund.succeeded"
	// Ensure these match NotificationType for clarity, or use a single enum if appropriate
)

// Notification represents the structure of an incoming webhook notification.
type Notification struct {
	Type   NotificationType  `json:"type"`   // e.g., "notification"
	Event  NotificationEvent `json:"event"`  // e.g., "payment.succeeded"
	Object PaymentResponse   `json:"object"` // The payment or refund object related to the event
}

// ErrorResponse represents an error response from the YooKassa API.
type ErrorResponse struct {
	Type        string `json:"type"`                  // e.g., "error"
	ID          string `json:"id"`                    // Request ID
	Code        string `json:"code"`                  // Error code (e.g., "invalid_request", "not_found")
	Description string `json:"description"`           // Human-readable description
	Parameter   string `json:"parameter,omitempty"`   // Parameter that caused the error
	RetryAfter  int    `json:"retry_after,omitempty"` // Seconds to wait before retrying (for rate limits)
}

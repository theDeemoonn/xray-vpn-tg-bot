package apperrors

import (
	"fmt"
)

// Error codes represent specific categories of application errors.
type ErrorCode string

const (
	ErrCodeUnknown      ErrorCode = "UNKNOWN"      // Неизвестная или общая ошибка
	ErrCodeNotFound     ErrorCode = "NOT_FOUND"    // Ресурс не найден
	ErrCodeValidation   ErrorCode = "VALIDATION"   // Ошибка валидации данных
	ErrCodeUnauthorized ErrorCode = "UNAUTHORIZED" // Ошибка авторизации (не аутентифицирован)
	ErrCodeForbidden    ErrorCode = "FORBIDDEN"    // Ошибка доступа (недостаточно прав)
	ErrCodeInternal     ErrorCode = "INTERNAL"     // Внутренняя ошибка сервера
	ErrCodeConflict     ErrorCode = "CONFLICT"     // Конфликт состояния (например, ресурс уже существует)
)

// Error represents a custom application error.
type Error struct {
	Code    ErrorCode // Category of the error
	Message string    // User-friendly message
	Details any       // Optional additional details (e.g., validation failures)
	cause   error     // Optional underlying error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s -> %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap provides compatibility for errors.Is and errors.As.
func (e *Error) Unwrap() error {
	return e.cause
}

// --- Constructors --- //

// New creates a new Error with a specific code, message, and optional cause.
func New(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// NewNotFoundError creates a new error with NOT_FOUND code.
// Accepts resource name and identifier for a more specific message.
func NewNotFoundError(resource string, identifier string, cause error) *Error {
	message := fmt.Sprintf("%s с идентификатором '%s' не найден(а)", resource, identifier)
	return New(ErrCodeNotFound, message, cause)
}

// NewValidationError creates a new error with VALIDATION code and optional details/cause.
func NewValidationError(message string, details any, cause ...error) *Error {
	var underlying error
	if len(cause) > 0 {
		underlying = cause[0]
	}
	return &Error{Code: ErrCodeValidation, Message: message, Details: details, cause: underlying}
}

// NewInternalError creates a new error with INTERNAL code and optional cause.
func NewInternalError(message string, cause error) *Error {
	return New(ErrCodeInternal, message, cause)
}

// NewConflictError creates a new error with CONFLICT code and optional cause.
func NewConflictError(message string, cause error) *Error {
	return New(ErrCodeConflict, message, cause)
}

// NewForbiddenError creates a new error with FORBIDDEN code and optional cause.
func NewForbiddenError(message string, cause error) *Error {
	return New(ErrCodeForbidden, message, cause)
}

// --- Predefined Errors --- //

// Predefined errors for common application scenarios
var (
	// Common Not Found errors (using constructor with generic message)
	ErrUserNotFound         = NewNotFoundError("Пользователь", "", nil)
	ErrPlanNotFound         = NewNotFoundError("Тарифный план", "", nil)
	ErrServerNotFound       = NewNotFoundError("Сервер", "", nil)
	ErrSubscriptionNotFound = NewNotFoundError("Подписка", "", nil)
	ErrPaymentNotFound      = NewNotFoundError("Платеж", "", nil)
	ErrInboundNotFound      = NewNotFoundError("Настройки подключения (inbound)", "", nil)

	// Other common errors
	ErrForbidden              = NewForbiddenError("Доступ запрещен", nil)
	ErrSubscriptionInactive   = NewConflictError("Подписка неактивна", nil)
	ErrSubscriptionConfigured = NewConflictError("Подписка уже настроена на сервер", nil)

	// Add more predefined errors as needed
)

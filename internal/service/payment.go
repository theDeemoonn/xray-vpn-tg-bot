package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"

	"go.mongodb.org/mongo-driver/bson/primitive"
	// "github.com/google/uuid" // No longer needed for IDs here
	// "xray-vpn-tg-bot/pkg/yookassa" // No longer needed
)

// Remove old service-level errors
// var (
// 	ErrPaymentNotFound         = errors.New("payment not found")
// 	ErrPlanNotFoundForPayment  = errors.New("plan not found for payment") // Renamed
// 	ErrPaymentInvalidPayload   = errors.New("invalid payment payload")
// 	ErrPaymentAlreadyProcessed = errors.New("payment already processed")
// )

// PaymentService defines the interface for Telegram Payment operations.
type PaymentService interface {
	CreatePendingPayment(ctx context.Context, userID, planID primitive.ObjectID) (payload string, err error)
	ConfirmPreCheckout(ctx context.Context, payload string) (*domain.Plan, error)
	ProcessSuccessfulPayment(ctx context.Context, payload string, providerChargeID string) error
	GetPaymentStatus(ctx context.Context, paymentID primitive.ObjectID) (domain.PaymentStatus, error)
	InitiatePayment(ctx context.Context, userID, planID primitive.ObjectID) (paymentIDHex string, err error)
}

// Payments implements the PaymentService interface.
type Payments struct {
	paymentRepo         repository.PaymentRepository
	planRepo            repository.PlanRepository
	subscriptionRepo    repository.SubscriptionRepository
	userRepo            repository.UserRepository
	subscriptionService SubscriptionService
	logger              *slog.Logger
	// yookassaClient *yookassa.Client // Removed
	// webhookReturnURL string // Removed
}

// NewPaymentService creates a new instance of Payments service for Telegram Payments.
func NewPaymentService(
	paymentRepo repository.PaymentRepository,
	planRepo repository.PlanRepository,
	subscriptionRepo repository.SubscriptionRepository,
	userRepo repository.UserRepository,
	subscriptionService SubscriptionService,
	logger *slog.Logger,
) PaymentService { // Return interface type
	return &Payments{
		paymentRepo:         paymentRepo,
		planRepo:            planRepo,
		subscriptionRepo:    subscriptionRepo,
		userRepo:            userRepo,
		subscriptionService: subscriptionService,
		logger:              logger.With(slog.String("service", "payment")),
	}
}

// CreatePendingPayment creates a pending payment record in the local DB
// and returns its ID as the payload for Telegram Invoice.
func (s *Payments) CreatePendingPayment(ctx context.Context, userID, planID primitive.ObjectID) (string, error) {
	s.logger.InfoContext(ctx, "Creating pending payment record", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) { // Use apperrors
			s.logger.WarnContext(ctx, "Plan not found for pending payment creation", slog.String("plan_id", planID.Hex()))
			return "", err // Return the original ErrPlanNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get plan for pending payment", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		// Return InternalError wrapping the original error
		return "", apperrors.NewInternalError("не удалось получить информацию о тарифном плане", err)
	}

	// Create local payment record with pending status
	paymentID := primitive.NewObjectID()
	localPayment := domain.Payment{
		ID:       paymentID,
		UserID:   userID,
		PlanID:   planID,
		Amount:   plan.Price,
		Currency: plan.Currency,
		Status:   domain.PaymentStatusPending,
		Provider: domain.PaymentProviderYooKassaTelegram,
		// ProviderPaymentID is set on successful payment
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.paymentRepo.Create(ctx, &localPayment); err != nil {
		s.logger.ErrorContext(ctx, "Failed to save pending payment record", slog.Any("error", err), slog.Any("payment", localPayment))
		// Return InternalError wrapping the original error
		return "", apperrors.NewInternalError("не удалось сохранить запись о платеже", err)
	}

	payload := paymentID.Hex()
	s.logger.InfoContext(ctx, "Pending payment created successfully", slog.String("payment_id", payload))
	return payload, nil
}

// InitiatePayment creates a payment record with pending status and returns its ID as a hex string.
// This ID should be used as the payload for Telegram SendInvoice.
func (s *Payments) InitiatePayment(ctx context.Context, userID, planID primitive.ObjectID) (string, error) {
	s.logger.InfoContext(ctx, "Initiating payment", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) { // Use apperrors
			s.logger.WarnContext(ctx, "Plan not found for payment initiation", slog.String("plan_id", planID.Hex()))
			return "", err // Return the original ErrPlanNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get plan for payment initiation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return "", apperrors.NewInternalError("не удалось получить информацию о тарифном плане", err)
	}

	if !plan.IsActive {
		s.logger.WarnContext(ctx, "Attempted to initiate payment for inactive plan", slog.String("plan_id", planID.Hex()))
		return "", apperrors.NewValidationError("выбранный тарифный план больше не доступен", plan.ID.Hex(), nil)
	}

	paymentID := primitive.NewObjectID()
	// Генерируем временный уникальный идентификатор для ProviderPaymentID
	tempProviderID := fmt.Sprintf("pending_%s_%d", paymentID.Hex(), time.Now().UnixNano())

	localPayment := &domain.Payment{
		ID:                paymentID,
		UserID:            userID,
		PlanID:            planID,
		Amount:            plan.Price,    // Store the actual price at the time of initiation
		Currency:          plan.Currency, // Store the currency
		Status:            domain.PaymentStatusPending,
		Provider:          domain.PaymentProviderTelegram, // Assuming Telegram Payments
		ProviderPaymentID: tempProviderID,                 // Устанавливаем временный уникальный ID
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := s.paymentRepo.Create(ctx, localPayment); err != nil {
		s.logger.ErrorContext(ctx, "Failed to save initiated payment record", slog.Any("error", err), slog.Any("payment", localPayment))
		return "", apperrors.NewInternalError("не удалось сохранить запись о платеже", err)
	}

	payload := paymentID.Hex()
	s.logger.InfoContext(ctx, "Payment initiated successfully", slog.String("payment_id", payload))
	return payload, nil
}

// ConfirmPreCheckout checks if the payment associated with the payload is valid
// and if the plan is still active.
func (s *Payments) ConfirmPreCheckout(ctx context.Context, payload string) (*domain.Plan, error) {
	s.logger.InfoContext(ctx, "Confirming pre-checkout query", slog.String("payload", payload))

	paymentID, err := primitive.ObjectIDFromHex(payload)
	if err != nil {
		s.logger.WarnContext(ctx, "Invalid payload format received in pre-checkout", slog.String("payload", payload), slog.Any("error", err))
		// Return ValidationError
		return nil, apperrors.NewValidationError("неверный формат идентификатора платежа", payload, err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, paymentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPaymentNotFound) { // Use apperrors
			s.logger.WarnContext(ctx, "Payment not found for pre-checkout payload", slog.String("payload", payload))
			return nil, err // Return the original ErrPaymentNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get payment for pre-checkout", slog.String("payload", payload), slog.Any("error", err))
		// Return InternalError wrapping the original error
		return nil, apperrors.NewInternalError("не удалось получить информацию о платеже", err)
	}

	// Check if payment is still pending
	if payment.Status != domain.PaymentStatusPending {
		s.logger.WarnContext(ctx, "Pre-checkout for non-pending payment", slog.String("payload", payload), slog.String("status", string(payment.Status)))
		// Return ConflictError (or maybe ValidationError? Conflict seems better)
		msg := fmt.Sprintf("платеж уже обработан или отменен (статус: %s)", payment.Status)
		return nil, apperrors.NewConflictError(msg, nil)
	}

	// Check if the plan still exists and is active
	plan, err := s.planRepo.GetByID(ctx, payment.PlanID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) { // Use apperrors
			s.logger.ErrorContext(ctx, "Plan associated with payment not found during pre-checkout", slog.String("payload", payload), slog.String("plan_id", payment.PlanID.Hex()))
			return nil, err // Return the original ErrPlanNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get plan during pre-checkout", slog.String("payload", payload), slog.String("plan_id", payment.PlanID.Hex()), slog.Any("error", err))
		// Return InternalError wrapping the original error
		return nil, apperrors.NewInternalError("не удалось получить информацию о тарифном плане", err)
	}

	if !plan.IsActive {
		s.logger.WarnContext(ctx, "Plan associated with payment is no longer active", slog.String("payload", payload), slog.String("plan_id", plan.ID.Hex()))
		// Return ConflictError or ValidationError
		msg := fmt.Sprintf("тарифный план '%s' больше не доступен", plan.Name)
		return nil, apperrors.NewConflictError(msg, nil)
	}

	s.logger.InfoContext(ctx, "Pre-checkout confirmed successfully", slog.String("payload", payload))
	return plan, nil
}

// ProcessSuccessfulPayment updates the payment status to succeeded, saves the provider ID,
// and triggers subscription activation via SubscriptionService.
func (s *Payments) ProcessSuccessfulPayment(ctx context.Context, payload string, providerChargeID string) error {
	s.logger.InfoContext(ctx, "Processing successful payment", slog.String("payload", payload), slog.String("provider_charge_id", providerChargeID))

	paymentID, err := primitive.ObjectIDFromHex(payload)
	if err != nil {
		s.logger.ErrorContext(ctx, "Invalid payload format received in successful payment", slog.String("payload", payload), slog.Any("error", err))
		// Return ValidationError
		return apperrors.NewValidationError("неверный формат идентификатора платежа", payload, err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, paymentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPaymentNotFound) { // Use apperrors
			s.logger.ErrorContext(ctx, "Payment not found for successful payment payload", slog.String("payload", payload))
			return err // Return the original ErrPaymentNotFound (critical)
		}
		s.logger.ErrorContext(ctx, "Failed to get payment for successful payment processing", slog.String("payload", payload), slog.Any("error", err))
		// Return InternalError wrapping the original error
		return apperrors.NewInternalError("не удалось получить информацию о платеже", err)
	}

	// Check if already processed (idempotency check)
	if payment.Status == domain.PaymentStatusSucceeded {
		s.logger.WarnContext(ctx, "Received successful payment notification for already succeeded payment", slog.String("payload", payload))
		// Return ConflictError to signal idempotency issue
		return apperrors.NewConflictError("этот платеж уже был успешно обработан", nil)
	}

	// Check if it was pending
	if payment.Status != domain.PaymentStatusPending {
		s.logger.ErrorContext(ctx, "Received successful payment notification for payment not in pending state",
			slog.String("payload", payload), slog.String("status", string(payment.Status)))
		// Return ConflictError
		msg := fmt.Sprintf("невозможно обработать успешный платеж со статусом '%s'", payment.Status)
		return apperrors.NewConflictError(msg, nil)
	}

	// Update payment record
	payment.Status = domain.PaymentStatusSucceeded
	payment.ProviderPaymentID = providerChargeID
	payment.UpdatedAt = time.Now()

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		s.logger.ErrorContext(ctx, "Failed to update payment status to succeeded", slog.String("payment_id", payment.ID.Hex()), slog.Any("error", err))
		// Return InternalError, but log that activation will still be attempted
		s.logger.WarnContext(ctx, "Database update failed, but attempting subscription activation anyway", slog.String("payment_id", payment.ID.Hex()))
		// Don't return here, proceed to activation
	}
	s.logger.InfoContext(ctx, "Payment status updated to succeeded", slog.String("payment_id", payment.ID.Hex()))

	// Activate the subscription via SubscriptionService
	s.logger.InfoContext(ctx, "Payment succeeded, triggering subscription activation", slog.String("payment_id", payment.ID.Hex()))
	err = s.subscriptionService.ActivateSubscription(ctx, payment.UserID, payment.PlanID, payment.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "Subscription activation failed after successful payment",
			slog.String("payment_id", payment.ID.Hex()),
			slog.String("user_id", payment.UserID.Hex()),
			slog.String("plan_id", payment.PlanID.Hex()),
			slog.Any("error", err))
		// Return InternalError wrapping the activation error
		// Important to signal that something went wrong after payment
		return apperrors.NewInternalError("не удалось активировать подписку после успешной оплаты", err)
	}

	s.logger.InfoContext(ctx, "Subscription activation triggered successfully", slog.String("payment_id", payment.ID.Hex()))
	return nil
}

// GetPaymentStatus retrieves the status of a payment from the local database.
func (s *Payments) GetPaymentStatus(ctx context.Context, paymentID primitive.ObjectID) (domain.PaymentStatus, error) {
	s.logger.DebugContext(ctx, "Getting payment status", slog.String("payment_id", paymentID.Hex()))
	payment, err := s.paymentRepo.GetByID(ctx, paymentID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPaymentNotFound) { // Use apperrors
			s.logger.WarnContext(ctx, "Attempted to get status for unknown payment", slog.String("payment_id", paymentID.Hex()))
			return "", err // Return the original ErrPaymentNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get payment by ID", slog.String("payment_id", paymentID.Hex()), slog.Any("error", err))
		// Return InternalError wrapping the original error
		return "", apperrors.NewInternalError("не удалось получить статус платежа", err)
	}
	return payment.Status, nil
}

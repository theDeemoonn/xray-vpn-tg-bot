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
	"xray-vpn-tg-bot/internal/xui"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type subscriptionService struct {
	subRepo      repository.SubscriptionRepository
	planRepo     repository.PlanRepository
	serverRepo   repository.ServerRepository
	userService  UserService
	logger       *slog.Logger
	configurator *subscriptionConfigurator
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subRepo repository.SubscriptionRepository,
	planRepo repository.PlanRepository,
	serverRepo repository.ServerRepository,
	userService UserService,
	xuiClientFactory func(server *domain.Server) (*xui.Client, error),
	logger *slog.Logger,
	userRepo repository.UserRepository,
) SubscriptionService {
	configurator := newSubscriptionConfigurator(
		subRepo,
		serverRepo,
		userRepo,
		xuiClientFactory,
		logger,
	)

	return &subscriptionService{
		subRepo:      subRepo,
		planRepo:     planRepo,
		serverRepo:   serverRepo,
		userService:  userService,
		logger:       logger.With(slog.String("service", "subscription")),
		configurator: configurator,
	}
}

// ActivateSubscription creates or extends a user's subscription record in the database after successful payment.
func (s *subscriptionService) ActivateSubscription(ctx context.Context, userID, planID, paymentID primitive.ObjectID) error {
	s.logger.InfoContext(ctx, "Activating subscription DB record", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during DB activation", slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))
			return err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription DB activation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return err
	}

	existingSub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil && !errors.Is(err, apperrors.ErrSubscriptionNotFound) {
		s.logger.ErrorContext(ctx, "Failed to check for existing subscription during activation", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)
	var subToConfigure *domain.Subscription

	if existingSub != nil {
		if existingSub.Status == domain.SubscriptionStatusActive && existingSub.ExpiresAt.After(now) {
			newExpiryDate = existingSub.ExpiresAt.Add(plan.Duration)
			s.logger.InfoContext(ctx, "Extending existing active subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("old_expiry", existingSub.ExpiresAt), slog.Time("new_expiry", newExpiryDate))
		} else {
			s.logger.InfoContext(ctx, "Reactivating/overwriting existing subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("new_expiry", newExpiryDate))
		}

		existingSub.PlanID = planID
		existingSub.ExpiresAt = newExpiryDate
		existingSub.Status = domain.SubscriptionStatusActive
		existingSub.ActivatedAt = now
		existingSub.UpdatedAt = now
		existingSub.PaymentID = paymentID
		existingSub.TrafficLimit = int64(plan.TrafficGB) * 1024 * 1024 * 1024
		existingSub.TrafficUsed = 0
		existingSub.ServerID = primitive.NilObjectID
		existingSub.XuiInboundID = 0
		existingSub.XuiClientUID = ""
		existingSub.ConfigLink = ""
		existingSub.XuiClientUUID = ""

		if err := s.subRepo.Update(ctx, existingSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to update existing subscription DB record", slog.String("sub_id", existingSub.ID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "Subscription DB record updated successfully", slog.String("sub_id", existingSub.ID.Hex()))
		subToConfigure = existingSub

	} else {
		s.logger.InfoContext(ctx, "Creating new subscription DB record", slog.String("user_id", userID.Hex()), slog.Time("expiry", newExpiryDate))
		newSub := &domain.Subscription{
			ID:           primitive.NewObjectID(),
			UserID:       userID,
			PlanID:       planID,
			Status:       domain.SubscriptionStatusActive,
			ActivatedAt:  now,
			ExpiresAt:    newExpiryDate,
			AutoRenew:    false,
			PaymentID:    paymentID,
			CreatedAt:    now,
			UpdatedAt:    now,
			TrafficLimit: int64(plan.TrafficGB) * 1024 * 1024 * 1024,
			TrafficUsed:  0,
		}
		if err := s.subRepo.Create(ctx, newSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to create new subscription DB record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "New subscription DB record created successfully", slog.String("sub_id", newSub.ID.Hex()))
		subToConfigure = newSub
	}

	if subToConfigure != nil {
		s.logger.InfoContext(ctx, "Subscription needs server configuration", slog.String("sub_id", subToConfigure.ID.Hex()))
		servers, err := s.serverRepo.GetAllEnabled(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get enabled servers for auto-configuration", slog.Any("error", err))
			s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but failed to find server. Manual intervention needed.", slog.String("sub_id", subToConfigure.ID.Hex()))
			return nil
		}
		if len(servers) == 0 {
			s.logger.ErrorContext(ctx, "CRITICAL: No enabled servers found for auto-configuration. Subscription activated in DB without server.", slog.String("sub_id", subToConfigure.ID.Hex()))
			return nil
		}
		selectedServer := servers[0]

		s.logger.InfoContext(ctx, "Attempting to auto-configure server for subscription", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
		configErr := s.configurator.ConfigureSubscriptionServer(ctx, userID, subToConfigure.ID, selectedServer.ID)
		if configErr != nil {
			s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but auto-configuration failed.", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()), slog.Any("config_error", configErr))
			return nil
		}
		s.logger.InfoContext(ctx, "Auto-configuration successful", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
	} else {
		s.logger.ErrorContext(ctx, "Internal logic error: subToConfigure was nil after DB update/create", slog.String("user_id", userID.Hex()))
	}

	s.logger.InfoContext(ctx, "Subscription DB activation/update process completed", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))
	return nil
}

// GetUserActiveSubscription retrieves the active subscription for a user.
func (s *subscriptionService) GetUserActiveSubscription(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting active subscription for user", slog.String("user_id", userID.Hex()))
	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.DebugContext(ctx, "No active subscription found for user", slog.String("user_id", userID.Hex()))
			return nil, nil
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}
	return sub, nil
}

// GetSubscriptionByID retrieves a subscription by its ID.
func (s *subscriptionService) GetSubscriptionByID(ctx context.Context, subID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting subscription by ID", slog.String("sub_id", subID.Hex()))
	sub, err := s.subRepo.GetByID(ctx, subID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Subscription not found by ID", slog.String("sub_id", subID.Hex()))
			return nil, apperrors.ErrSubscriptionNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get subscription by ID from repository", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		return nil, err
	}
	return sub, nil
}

// ConfigureSubscriptionServer delegates to the configurator.
func (s *subscriptionService) ConfigureSubscriptionServer(ctx context.Context, userID, subID, serverID primitive.ObjectID) error {
	return s.configurator.ConfigureSubscriptionServer(ctx, userID, subID, serverID)
}

// GetConfigLink delegates to the configurator.
func (s *subscriptionService) GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error) {
	return s.configurator.GetConfigLink(ctx, sub)
}

// GetSubscriptionQRCode delegates to the configurator.
func (s *subscriptionService) GetSubscriptionQRCode(ctx context.Context, sub *domain.Subscription) ([]byte, error) {
	return s.configurator.GetSubscriptionQRCode(ctx, sub)
}

// FindAndExpireSubscriptions находит и обновляет статус подписок, которые истекли
func (s *subscriptionService) FindAndExpireSubscriptions(ctx context.Context) error {
	now := time.Now()
	s.logger.InfoContext(ctx, "Проверка и обновление истекших подписок", slog.Time("now", now))

	filter := bson.M{
		"status": domain.SubscriptionStatusActive,
		"expires_at": bson.M{
			"$lt": now,
		},
		"auto_renew": false,
	}

	subscriptions, err := s.subRepo.FindByFilter(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "Ошибка при поиске истекших подписок", slog.Any("error", err))
		return err
	}

	s.logger.InfoContext(ctx, "Найдены истекшие подписки", slog.Int("count", len(subscriptions)))

	for _, sub := range subscriptions {
		oldStatus := sub.Status
		sub.Status = domain.SubscriptionStatusExpired
		sub.UpdatedAt = now

		if err := s.subRepo.Update(ctx, sub); err != nil {
			s.logger.ErrorContext(ctx, "Ошибка при обновлении статуса подписки",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("old_status", string(oldStatus)),
				slog.String("new_status", string(sub.Status)),
				slog.Any("error", err))
			continue
		}

		s.logger.InfoContext(ctx, "Подписка успешно помечена как истекшая",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("user_id", sub.UserID.Hex()))
	}

	return nil
}

// CreateSubscription создает новую подписку для пользователя
func (s *subscriptionService) CreateSubscription(ctx context.Context, userID primitive.ObjectID, planID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.InfoContext(ctx, "Creating new subscription", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during subscription creation", slog.String("plan_id", planID.Hex()))
			return nil, err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription creation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return nil, err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)

	newSub := &domain.Subscription{
		ID:           primitive.NewObjectID(),
		UserID:       userID,
		PlanID:       planID,
		Status:       domain.SubscriptionStatusPending,
		ActivatedAt:  now,
		ExpiresAt:    newExpiryDate,
		AutoRenew:    false,
		CreatedAt:    now,
		UpdatedAt:    now,
		TrafficLimit: int64(plan.TrafficGB) * 1024 * 1024 * 1024,
		TrafficUsed:  0,
	}

	if err := s.subRepo.Create(ctx, newSub); err != nil {
		s.logger.ErrorContext(ctx, "Failed to create new subscription record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, err
	}

	s.logger.InfoContext(ctx, "New subscription created successfully (needs configuration)", slog.String("sub_id", newSub.ID.Hex()))
	return newSub, nil
}

// GetActiveSubscriptionForUser получает список активных подписок для пользователя
func (s *subscriptionService) GetActiveSubscriptionForUser(ctx context.Context, userID primitive.ObjectID) ([]*domain.SubscriptionDetails, error) {
	s.logger.DebugContext(ctx, "Getting active subscriptions for user", slog.String("user_id", userID.Hex()))

	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			s.logger.DebugContext(ctx, "No active subscriptions found for user", slog.String("user_id", userID.Hex()))
			return []*domain.SubscriptionDetails{}, nil
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}

	if sub != nil {
		plan, err := s.planRepo.GetByID(ctx, sub.PlanID)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get plan for subscription details", slog.String("sub_id", sub.ID.Hex()), slog.String("plan_id", sub.PlanID.Hex()), slog.Any("error", err))
		}

		var serverName string
		if !sub.ServerID.IsZero() {
			server, err := s.serverRepo.GetByID(ctx, sub.ServerID)
			if err == nil && server != nil {
				serverName = server.Name
			} else if err != nil {
				s.logger.WarnContext(ctx, "Failed to get server name for subscription details", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
			}
		}

		details := &domain.SubscriptionDetails{
			Subscription: sub,
			PlanName:     "Неизвестный план",
			ServerName:   serverName,
		}

		if plan != nil {
			details.PlanName = plan.Name
		}

		return []*domain.SubscriptionDetails{details}, nil
	}

	return []*domain.SubscriptionDetails{}, nil
}

// UpdateTrafficStats обновляет информацию о трафике и дате окончания подписки
func (s *subscriptionService) UpdateTrafficStats(ctx context.Context, sub *domain.Subscription) error {
	s.logger.InfoContext(ctx, "Updating subscription traffic statistics",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int64("traffic_used", sub.TrafficUsed),
		slog.Int64("traffic_limit", sub.TrafficLimit),
		slog.Time("expires_at", sub.ExpiresAt))

	// Обновляем время последнего изменения
	sub.UpdatedAt = time.Now()

	// Сохраняем изменения в базе данных
	err := s.subRepo.Update(ctx, sub)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update subscription traffic stats in repository",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
		return err
	}

	s.logger.Debug("Subscription traffic statistics updated successfully",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int64("traffic_used", sub.TrafficUsed))
	return nil
}

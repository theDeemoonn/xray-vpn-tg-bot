package service

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
)

// UserService defines the interface for user-related operations.
type UserService interface {
	GetOrCreate(ctx context.Context, tgID int64, isBot bool, firstName, lastName, username, languageCode string, isPremium bool) (*domain.User, error)
	GetByTelegramID(ctx context.Context, id int64) (*domain.User, error)
	// Add other user methods here (e.g., GetByID, UpdateBalance, etc.)
}

type userService struct {
	userRepo repository.UserRepository
	logger   *slog.Logger
}

func NewUserService(userRepo repository.UserRepository, logger *slog.Logger) UserService {
	return &userService{
		userRepo: userRepo,
		logger:   logger.With(slog.String("service", "user")),
	}
}

// GetOrCreate retrieves a user by Telegram ID or creates a new one if not found.
// It also updates user information (username, names, premium status) if the user exists.
func (s *userService) GetOrCreate(ctx context.Context, tgID int64, isBot bool, firstName, lastName, username, languageCode string, isPremium bool) (*domain.User, error) {
	if tgID == 0 {
		return nil, apperrors.NewValidationError("Telegram ID не может быть нулевым", nil)
	}
	s.logger.DebugContext(ctx, "GetOrCreate called", slog.Int64("tg_id", tgID))

	existingUser, err := s.userRepo.GetByTelegramID(ctx, tgID)

	// Declare appErr here to use in the switch
	var appErr *apperrors.Error

	switch {
	case err == nil:
		s.logger.DebugContext(ctx, "User found, checking for updates", slog.String("user_id", existingUser.ID.Hex()), slog.Int64("tg_id", tgID))
		return s.updateUserInfoIfNeeded(ctx, existingUser, tgID, isBot, firstName, lastName, username, languageCode, isPremium)

	case errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound:
		s.logger.InfoContext(ctx, "User not found, creating new user", slog.Int64("tg_id", tgID))
		return s.createNewUser(ctx, tgID, isBot, firstName, lastName, username, languageCode, isPremium)

	default:
		s.logger.ErrorContext(ctx, "Failed to get user by telegram id", slog.Int64("tg_id", tgID), slog.Any("error", err))
		return nil, apperrors.NewInternalError("Не удалось получить пользователя по Telegram ID", err)
	}
}

// createNewUser handles the creation of a new user.
func (s *userService) createNewUser(ctx context.Context, tgID int64, isBot bool, firstName, lastName, username, languageCode string, isPremium bool) (*domain.User, error) {
	newUser := domain.NewUser(
		tgID,
		username,
		firstName,
		lastName,
		languageCode,
		isBot,
		isPremium,
	)
	// Generate initial referral code
	newUser.ReferralCode = s.generateReferralCode(ctx)

	err := s.userRepo.Create(ctx, newUser)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to create new user in repository", slog.Int64("tg_id", tgID), slog.Any("error", err))
		return nil, err
	}
	s.logger.InfoContext(ctx, "New user created successfully", slog.String("user_id", newUser.ID.Hex()), slog.Int64("tg_id", newUser.TelegramID))
	return newUser, nil
}

// updateUserInfoIfNeeded checks if user info from Telegram differs from the stored one and updates if necessary.
func (s *userService) updateUserInfoIfNeeded(ctx context.Context, existingUser *domain.User, tgID int64, isBot bool, firstName, lastName, username, languageCode string, isPremium bool) (*domain.User, error) {
	updated := false
	if existingUser.Username != username {
		s.logger.DebugContext(ctx, "Username changed", slog.String("user_id", existingUser.ID.Hex()), slog.String("old", existingUser.Username), slog.String("new", username))
		existingUser.Username = username
		updated = true
	}
	if existingUser.FirstName != firstName {
		s.logger.DebugContext(ctx, "First name changed", slog.String("user_id", existingUser.ID.Hex()), slog.String("old", existingUser.FirstName), slog.String("new", firstName))
		existingUser.FirstName = firstName
		updated = true
	}
	if existingUser.LastName != lastName {
		s.logger.DebugContext(ctx, "Last name changed", slog.String("user_id", existingUser.ID.Hex()), slog.String("old", existingUser.LastName), slog.String("new", lastName))
		existingUser.LastName = lastName
		updated = true
	}
	if existingUser.IsPremium != isPremium {
		s.logger.DebugContext(ctx, "Premium status changed", slog.String("user_id", existingUser.ID.Hex()), slog.Bool("old", existingUser.IsPremium), slog.Bool("new", isPremium))
		existingUser.IsPremium = isPremium
		updated = true
	}
	if languageCode != "" && existingUser.LanguageCode != languageCode {
		s.logger.DebugContext(ctx, "Language code changed", slog.String("user_id", existingUser.ID.Hex()), slog.String("old", existingUser.LanguageCode), slog.String("new", languageCode))
		existingUser.LanguageCode = languageCode
		updated = true
	}
	// We don't update IsBot status usually

	if updated {
		s.logger.InfoContext(ctx, "Updating existing user info", slog.String("user_id", existingUser.ID.Hex()), slog.Int64("tg_id", existingUser.TelegramID))
		err := s.userRepo.Update(ctx, existingUser)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to update user info in repository", slog.String("user_id", existingUser.ID.Hex()), slog.Int64("tg_id", existingUser.TelegramID), slog.Any("error", err))
			return nil, err
		}
		s.logger.InfoContext(ctx, "User info updated successfully", slog.String("user_id", existingUser.ID.Hex()))
	} else {
		s.logger.DebugContext(ctx, "User info is up-to-date", slog.String("user_id", existingUser.ID.Hex()))
	}
	return existingUser, nil
}

// GetByTelegramID retrieves a user by Telegram ID.
func (s *userService) GetByTelegramID(ctx context.Context, id int64) (*domain.User, error) {
	s.logger.DebugContext(ctx, "GetByTelegramID called", slog.Int64("tg_id", id))
	user, err := s.userRepo.GetByTelegramID(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "Failed to get user by telegram ID from repo", slog.Int64("tg_id", id), slog.Any("error", err))
		return nil, err
	}
	return user, nil
}

// generateReferralCode generates a random referral code.
// In a real application, you'd want to ensure uniqueness.
func (s *userService) generateReferralCode(ctx context.Context) string {
	const length = 8
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	code := make([]byte, length)
	for i := range code {
		code[i] = chars[rand.Intn(len(chars))] //nolint:gosec // Cryptographic randomness not strictly needed here
	}
	return string(code)
}

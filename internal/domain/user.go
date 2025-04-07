package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type User struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TelegramID    int64              `bson:"telegram_id" json:"telegram_id"`
	Username      string             `bson:"username,omitempty" json:"username,omitempty"` // Telegram username
	FirstName     string             `bson:"first_name,omitempty" json:"first_name,omitempty"`
	LastName      string             `bson:"last_name,omitempty" json:"last_name,omitempty"`
	IsBot         bool               `bson:"is_bot" json:"is_bot"`
	LanguageCode  string             `bson:"language_code,omitempty" json:"language_code,omitempty"`
	IsPremium     bool               `bson:"is_premium" json:"is_premium"`                           // Telegram Premium status
	Balance       float64            `bson:"balance" json:"balance"`                                 // Internal balance (e.g., for bonuses)
	ReferralCode  string             `bson:"referral_code,omitempty" json:"referral_code,omitempty"` // User's unique referral code
	ReferredBy    primitive.ObjectID `bson:"referred_by,omitempty" json:"referred_by,omitempty"`     // ID of the user who referred them
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time          `bson:"updated_at" json:"updated_at"`
	AgreedToTerms bool               `bson:"agreed_to_terms" json:"agreed_to_terms"` // Has the user agreed to terms?
	IsAdmin       bool               `bson:"is_admin" json:"is_admin"`               // Флаг администратора
}

// NewUser creates a new user instance
func NewUser(tgID int64, username, firstName, lastName, langCode string, isBot, isPremium bool) *User {
	return &User{
		TelegramID:    tgID,
		Username:      username,
		FirstName:     firstName,
		LastName:      lastName,
		IsBot:         isBot,
		LanguageCode:  langCode,
		IsPremium:     isPremium,
		Balance:       0,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		AgreedToTerms: false, // Default to false
		IsAdmin:       false, // По умолчанию не является администратором
	}
}

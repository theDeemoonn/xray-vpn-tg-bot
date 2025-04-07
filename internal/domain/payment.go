package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"   // Awaiting payment
	PaymentStatusSucceeded PaymentStatus = "succeeded" // Paid successfully
	PaymentStatusFailed    PaymentStatus = "failed"    // Payment failed
	PaymentStatusCancelled PaymentStatus = "cancelled" // Cancelled
)

type PaymentProvider string

const (
	PaymentProviderYooKassa         PaymentProvider = "yookassa"          // Old YooKassa via API
	PaymentProviderYooKassaWebhook  PaymentProvider = "yookassa_webhook"  // YooKassa via webhook
	PaymentProviderYooKassaTelegram PaymentProvider = "yookassa_telegram" // YooKassa via Telegram Payments (might rename)
	PaymentProviderTelegram         PaymentProvider = "telegram_payments" // Direct Telegram Payments
	// Add other providers if needed
)

// Payment represents a payment transaction
type Payment struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID            primitive.ObjectID `bson:"user_id" json:"user_id"`
	SubscriptionID    primitive.ObjectID `bson:"subscription_id,omitempty" json:"subscription_id,omitempty"` // Associated subscription (optional, for balance top-up)
	PlanID            primitive.ObjectID `bson:"plan_id,omitempty" json:"plan_id,omitempty"`                 // Associated plan (if paying for a specific plan)
	Amount            float64            `bson:"amount" json:"amount"`                                       // Payment amount
	Currency          string             `bson:"currency" json:"currency"`                                   // Currency code (e.g., "RUB")
	Provider          PaymentProvider    `bson:"provider" json:"provider"`                                   // Payment provider used
	ProviderPaymentID string             `bson:"provider_payment_id" json:"provider_payment_id"`             // Payment ID from the provider's system
	Status            PaymentStatus      `bson:"status" json:"status"`
	IsGift            bool               `bson:"is_gift" json:"is_gift"`                                         // Is this a gifted subscription payment?
	GiftRecipientID   primitive.ObjectID `bson:"gift_recipient_id,omitempty" json:"gift_recipient_id,omitempty"` // Recipient user ID if it's a gift
	Metadata          map[string]any     `bson:"metadata,omitempty" json:"metadata,omitempty"`                   // Additional data from the provider
	CreatedAt         time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt         time.Time          `bson:"updated_at" json:"updated_at"`
}

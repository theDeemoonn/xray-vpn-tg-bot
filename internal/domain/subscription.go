package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SubscriptionStatus string

const (
	SubscriptionStatusPending   SubscriptionStatus = "pending"   // Awaiting payment or activation
	SubscriptionStatusActive    SubscriptionStatus = "active"    // Active
	SubscriptionStatusExpired   SubscriptionStatus = "expired"   // Expired (time-based)
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled" // Cancelled by user
	SubscriptionStatusDepleted  SubscriptionStatus = "depleted"  // Expired (traffic-based)
)

// Subscription represents a user's subscription to a plan on a specific server
type Subscription struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID       primitive.ObjectID `bson:"user_id" json:"user_id"`
	PlanID       primitive.ObjectID `bson:"plan_id" json:"plan_id"`
	ServerID     primitive.ObjectID `bson:"server_id" json:"server_id"`           // Server where subscription is active
	XuiInboundID int                `bson:"xui_inbound_id" json:"xui_inbound_id"` // Inbound ID on the x-ui server
	XuiClientUID string             `bson:"xui_client_uid" json:"xui_client_uid"` // Client identifier (UUID/Email) in x-ui
	Status       SubscriptionStatus `bson:"status" json:"status"`
	ActivatedAt  time.Time          `bson:"activated_at,omitempty" json:"activated_at,omitempty"`   // Activation time
	ExpiresAt    time.Time          `bson:"expires_at" json:"expires_at"`                           // Expiration time
	AutoRenew    bool               `bson:"auto_renew" json:"auto_renew"`                           // Auto-renewal enabled?
	TrafficUsed  int64              `bson:"traffic_used,omitempty" json:"traffic_used,omitempty"`   // Traffic used in bytes (from x-ui)
	TrafficLimit int64              `bson:"traffic_limit,omitempty" json:"traffic_limit,omitempty"` // Traffic limit in bytes (0 = unlimited)
	PaymentID    primitive.ObjectID `bson:"payment_id,omitempty" json:"payment_id,omitempty"`       // Associated payment ID (if applicable)
	ConfigLink   string             `bson:"config_link,omitempty" json:"config_link,omitempty"`     // Generated VPN config link (e.g., vmess://...)
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at" json:"updated_at"`
}

package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Referral tracks users invited through the referral program
type Referral struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ReferrerID    primitive.ObjectID `bson:"referrer_id" json:"referrer_id"`         // User who invited
	InvitedUserID primitive.ObjectID `bson:"invited_user_id" json:"invited_user_id"` // User who was invited
	BonusAwarded  bool               `bson:"bonus_awarded" json:"bonus_awarded"`     // Was the bonus awarded to the referrer?
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
}

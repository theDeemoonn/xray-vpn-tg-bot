package domain

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Plan represents a subscription plan
type Plan struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name        string             `bson:"name" json:"name"` // e.g., "1 Month", "1 Year"
	Description string             `bson:"description,omitempty" json:"description,omitempty"`
	Price       float64            `bson:"price" json:"price"`           // Price (e.g., in RUB)
	Currency    string             `bson:"currency" json:"currency"`     // Currency code (e.g., "RUB")
	Duration    time.Duration      `bson:"duration" json:"duration"`     // Plan duration
	TrafficGB   int                `bson:"traffic_gb" json:"traffic_gb"` // Traffic limit in GB (0 for unlimited)
	IsActive    bool               `bson:"is_active" json:"is_active"`   // Available for purchase?
	SortOrder   int                `bson:"sort_order" json:"sort_order"` // Order in lists
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
}

// DurationString returns a human-readable representation of the duration
func (p *Plan) DurationString() string {
	hours := p.Duration.Hours()
	days := int(hours / 24)

	switch {
	case hours == 1:
		return "1 час"
	case hours == 3:
		return "3 часа"
	case hours == 12:
		return "12 часов"
	case days == 1:
		return "1 день"
	case days == 7:
		return "1 неделя"
	case days >= 28 && days <= 31:
		return "1 месяц"
	case days >= 89 && days <= 92:
		return "квартал"
	case days >= 360:
		return "1 год"
	default:
		// Fallback for other durations
		if days > 1 {
			return fmt.Sprintf("%d дней", days)
		}
		return fmt.Sprintf("%.0f часов", hours)
	}
}

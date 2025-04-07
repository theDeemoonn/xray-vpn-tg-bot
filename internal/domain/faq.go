package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FAQ represents a frequently asked question entry
type FAQ struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Category  string             `bson:"category" json:"category"` // e.g., "Payment", "Setup", "General"
	Question  string             `bson:"question" json:"question"`
	Answer    string             `bson:"answer" json:"answer"`
	IsActive  bool               `bson:"is_active" json:"is_active"`   // Show in the FAQ list?
	SortOrder int                `bson:"sort_order" json:"sort_order"` // Sort order within category
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

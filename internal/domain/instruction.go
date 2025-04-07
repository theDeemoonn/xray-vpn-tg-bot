package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Instruction represents a setup guide for a VPN client
type Instruction struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Platform  string             `bson:"platform" json:"platform"`     // e.g., "Android", "iOS", "Windows", "macOS", "Linux"
	ClientApp string             `bson:"client_app" json:"client_app"` // e.g., "V2RayNG", "Shadowrocket", "NekoBox"
	Title     string             `bson:"title" json:"title"`           // Instruction title
	Steps     []string           `bson:"steps" json:"steps"`           // Instruction steps
	IsActive  bool               `bson:"is_active" json:"is_active"`   // Is the instruction visible?
	SortOrder int                `bson:"sort_order" json:"sort_order"` // Display order
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Instruction represents a setup or usage instruction entry
type Instruction struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title     string             `bson:"title" json:"title"`           // e.g., "Настройка VLESS для Streisand"
	Content   string             `bson:"content" json:"content"`       // Detailed instruction text (Markdown supported?)
	Platform  string             `bson:"platform" json:"platform"`     // e.g., "iOS", "Android", "Windows", "Общее"
	IsActive  bool               `bson:"is_active" json:"is_active"`   // Show in the instruction list?
	SortOrder int                `bson:"sort_order" json:"sort_order"` // Sort order
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

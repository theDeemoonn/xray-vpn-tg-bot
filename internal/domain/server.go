package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ServerStatus string

const (
	ServerStatusActive   ServerStatus = "active"
	ServerStatusInactive ServerStatus = "inactive"
)

// Server represents a VPN server instance managed by x-ui
type Server struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Name            string             `bson:"name" json:"name"`                           // User-friendly name (e.g., "Netherlands-1")
	ApiHost         string             `bson:"api_host" json:"api_host"`                   // Host for X-UI API (e.g., http://1.2.3.4:54321)
	PublicHost      string             `bson:"public_host" json:"public_host"`             // Publicly accessible hostname or IP for VLESS links
	ApiUsername     string             `bson:"api_username" json:"-"`                      // Omit from JSON responses for security
	ApiPassword     string             `bson:"api_password" json:"-"`                      // Omit from JSON responses for security
	Location        string             `bson:"location" json:"location"`                   // Geographic location (e.g., "Amsterdam, NL")
	TargetInboundID int                `bson:"target_inbound_id" json:"target_inbound_id"` // Default Inbound ID in x-ui for new clients
	IsEnabled       bool               `bson:"is_enabled" json:"is_enabled"`               // Whether users can select this server
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
	LastCheck       time.Time          `bson:"last_check,omitempty" json:"last_check,omitempty"` // Last health check time
	IsHealthy       bool               `bson:"is_healthy" json:"is_healthy"`                     // Health status
	Status          ServerStatus       `bson:"status" json:"status"`
}

// NewServer creates a new server instance
func NewServer(name, apiHost, publicHost, apiUsername, apiPassword, location string, targetInboundID int) *Server {
	return &Server{
		Name:            name,
		ApiHost:         apiHost,
		PublicHost:      publicHost,
		ApiUsername:     apiUsername,
		ApiPassword:     apiPassword,
		Location:        location,
		TargetInboundID: targetInboundID,
		IsEnabled:       true, // Enabled by default
		IsHealthy:       true, // Assume healthy initially
		Status:          ServerStatusActive,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

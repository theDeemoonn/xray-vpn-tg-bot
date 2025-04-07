package domain

import "time"

// XUIConfig holds configuration related to x-ui interaction.
// This can be expanded with more fields if needed.
type XUIConfig struct {
	APITimeout time.Duration
}

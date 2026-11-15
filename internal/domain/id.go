package domain

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns a short random identifier (12 hex chars).
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Package id generates identifiers used across QueuePilot's domain tables.
package id

import "github.com/google/uuid"

// New returns a new random (v4) UUID as a string, suitable for any table's
// primary key.
func New() string {
	return uuid.NewString()
}

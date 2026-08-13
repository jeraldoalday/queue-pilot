package auth

import (
	"context"

	"github.com/jeraldoalday/queuepilot/internal/session"
)

// Logout revokes a session by deleting its row — it is invalid for any
// future request immediately, not just once its cookie expires.
func Logout(ctx context.Context, sessions *session.Store, token string) error {
	if token == "" {
		return nil
	}
	return sessions.Delete(ctx, token)
}

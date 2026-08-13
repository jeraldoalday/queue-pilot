package auth

import (
	"context"

	"github.com/jeraldoalday/queuepilot/internal/session"
)

// identityCtxKey carries the full session.Identity (name, email — display
// data, not authorization data) for handlers that need to render it.
// Authorization decisions must use tenant.OrgID/UserRole, not this.
type identityCtxKey struct{}

func withIdentity(ctx context.Context, ident session.Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, ident)
}

func CurrentIdentity(ctx context.Context) (session.Identity, bool) {
	ident, ok := ctx.Value(identityCtxKey{}).(session.Identity)
	return ident, ok
}

package tenant

import (
	"context"
	"errors"
)

// ErrNoIdentity means a domain function was called from a context that
// never went through RequireAuth (or LoadOrgBySlug, for public routes).
// This must never happen on a route wired correctly — treat it as a
// fail-closed 401/403 in the handler, not as "operate on the zero org".
var ErrNoIdentity = errors.New("tenant: no identity in context")

func RequireOrgID(ctx context.Context) (string, error) {
	v, ok := OrgID(ctx)
	if !ok {
		return "", ErrNoIdentity
	}
	return v, nil
}

func RequireUserID(ctx context.Context) (string, error) {
	v, ok := UserID(ctx)
	if !ok {
		return "", ErrNoIdentity
	}
	return v, nil
}

func RequireRole(ctx context.Context) (Role, error) {
	v, ok := UserRole(ctx)
	if !ok {
		return "", ErrNoIdentity
	}
	return v, nil
}

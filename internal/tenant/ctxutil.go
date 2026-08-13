// Package tenant carries the authenticated org/user identity through a
// request's context. It is the single source of truth for "which org is
// this request allowed to touch" — every domain service reads OrgID from
// here rather than accepting it as a caller-supplied argument, so there is
// no code path where a client-controlled value can select another
// tenant's data.
package tenant

import "context"

type contextKey int

const (
	orgIDKey contextKey = iota
	userIDKey
	roleKey
)

// Role mirrors the users.role check constraint.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleStaff Role = "staff"
)

func WithIdentity(ctx context.Context, orgID, userID string, role Role) context.Context {
	ctx = context.WithValue(ctx, orgIDKey, orgID)
	ctx = context.WithValue(ctx, userIDKey, userID)
	ctx = context.WithValue(ctx, roleKey, role)
	return ctx
}

// OrgID returns the authenticated org ID and whether one was set. Domain
// services should treat a missing OrgID as a fail-closed error, never a
// zero-value org.
func OrgID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(orgIDKey).(string)
	return v, ok && v != ""
}

func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok && v != ""
}

func UserRole(ctx context.Context) (Role, bool) {
	v, ok := ctx.Value(roleKey).(Role)
	return v, ok && v != ""
}

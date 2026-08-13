package auth

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type CreateStaffInput struct {
	Name     string
	Email    string
	Password string
}

func (in CreateStaffInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return ValidationError{"name is required"}
	}
	if !looksLikeEmail(in.Email) {
		return ValidationError{"a valid email is required"}
	}
	if len(in.Password) < 8 {
		return ValidationError{"password must be at least 8 characters"}
	}
	return nil
}

// CreateStaffUser adds a staff-role user to orgID. orgID must come from
// the caller's authenticated tenant context (see RequireOrgID in the
// handler) — never from a client-supplied field — so a staff account can
// only ever be created inside the admin's own org.
func CreateStaffUser(ctx context.Context, pool *pgxpool.Pool, orgID string, in CreateStaffInput) (sqlc.User, error) {
	if err := in.validate(); err != nil {
		return sqlc.User{}, err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlc.User{}, err
	}

	user, err := sqlc.New(pool).CreateUser(ctx, sqlc.CreateUserParams{
		ID:           db.MustUUID(id.New()),
		OrgID:        db.MustUUID(orgID),
		Email:        in.Email,
		Name:         in.Name,
		PasswordHash: hash,
		Role:         string(tenant.RoleStaff),
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			return sqlc.User{}, ErrEmailTaken
		}
		return sqlc.User{}, err
	}
	return user, nil
}

package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// Login verifies credentials and starts a new session. It deliberately
// returns the same ErrInvalidCredentials whether the email doesn't exist,
// the password is wrong, or the account is disabled — the caller's
// response to the user must not reveal which case occurred.
func Login(ctx context.Context, pool *pgxpool.Pool, sessions *session.Store, email, password, userAgent, ip string) (session.Identity, error) {
	q := sqlc.New(pool)

	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return session.Identity{}, ErrInvalidCredentials
		}
		return session.Identity{}, err
	}

	if user.DisabledAt.Valid || !VerifyPassword(user.PasswordHash, password) {
		return session.Identity{}, ErrInvalidCredentials
	}

	ident, err := sessions.Create(ctx, db.UUIDString(user.ID), db.UUIDString(user.OrgID), userAgent, ip)
	if err != nil {
		return session.Identity{}, err
	}
	ident.Email = user.Email
	ident.Name = user.Name
	ident.Role = tenant.Role(user.Role)
	return ident, nil
}

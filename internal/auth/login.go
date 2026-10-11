package auth

import (
	"context"
	"errors"
	"strings"

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

	// Trimmed the same way the stored address is: a mobile keyboard's
	// auto-capitalize-and-space or a pasted address with a trailing
	// newline must not turn a correct password into ErrInvalidCredentials.
	user, err := q.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No account to compare against, so compare against nothing:
			// returning here immediately would make an unknown address
			// answer visibly faster than a known one with the wrong
			// password, which tells a caller which addresses have
			// accounts. See EqualiseVerifyCost.
			EqualiseVerifyCost(password)
			return session.Identity{}, ErrInvalidCredentials
		}
		return session.Identity{}, err
	}

	// The comparison runs even for a disabled account, and its result is
	// combined afterwards rather than short-circuited. `disabled ||
	// !verify` would skip the bcrypt work whenever the account is
	// disabled, so a disabled account would answer faster than an active
	// one — separating "this address is disabled here" from "this address
	// is not registered here", which is exactly the distinction the shared
	// ErrInvalidCredentials exists to withhold.
	passwordOK := VerifyPassword(user.PasswordHash, password)
	if user.DisabledAt.Valid || !passwordOK {
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

package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
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

func ListStaff(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.ListStaffUsersByOrgRow, error) {
	return sqlc.New(pool).ListStaffUsersByOrg(ctx, db.MustUUID(orgID))
}

// ErrStaffNotFound means no staff account in this org matches the ID: it
// belongs to another tenant, it never existed, or it is an admin — which
// SetUserDisabled deliberately refuses to touch. All three read the same
// way to the caller, so a guessed ID reveals nothing about which it was.
var ErrStaffNotFound = errors.New("auth: no such staff account in this organisation")

// SetStaffDisabled suspends or restores one staff account.
//
// Suspension is what an organisation actually needs when someone leaves,
// and deletion is not it: appointments reference the staff member who
// performed them, tickets record who called them, and a day's report is
// read months later. Deleting the row would either destroy that history
// or be refused by the foreign keys holding it. A disabled account keeps
// every past row intact while being unable to sign in or hold a session.
//
// orgID must come from the caller's authenticated tenant context, so an
// admin can only ever suspend an account inside their own organisation.
//
// The account's live sessions are deleted in the same transaction as the
// suspension. Session lookup already refuses a disabled user, so this is
// not what makes the suspension take effect — it is what keeps the
// sessions table honest about who can still get in. Restoring an account
// does not restore its sessions: whoever it belongs to signs in again,
// which is the moment their password is checked afresh.
func SetStaffDisabled(ctx context.Context, pool *pgxpool.Pool, orgID, staffUserID string, disabled bool) (sqlc.User, error) {
	if !db.IsUUID(staffUserID) {
		return sqlc.User{}, ErrStaffNotFound
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return sqlc.User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	qtx := sqlc.New(tx)

	user, err := qtx.SetUserDisabled(ctx, sqlc.SetUserDisabledParams{
		ID:       db.MustUUID(staffUserID),
		OrgID:    db.MustUUID(orgID),
		Disabled: disabled,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, ErrStaffNotFound
		}
		return sqlc.User{}, err
	}

	if disabled {
		if _, err := qtx.DeleteSessionsForUser(ctx, user.ID); err != nil {
			return sqlc.User{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return sqlc.User{}, err
	}
	return user, nil
}

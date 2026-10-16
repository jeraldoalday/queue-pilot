package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/emailshape"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/platform/slug"
	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

var (
	ErrSlugTaken          = errors.New("auth: organization slug already taken")
	ErrEmailTaken         = errors.New("auth: email already registered")
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
)

// ValidationError wraps a message that is always safe to show a user
// directly (unlike unexpected DB/internal errors, which handlers must log
// and replace with a generic message before responding).
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

// maxOrgNameLen bounds the business name entered at signup. It mirrors
// org.maxNameLen — the same name is editable later from /app/settings and
// appears on every public page this organisation has.
const maxOrgNameLen = 100

// maxPersonNameLen bounds an admin's or staff member's own name.
const maxPersonNameLen = 100

// maxEmailLen is RFC 5321's own limit on an address, the same bound a
// customer's booking email is held to (see appointment.maxCustomerEmailLen).
const maxEmailLen = 254

type SignupInput struct {
	OrgName    string
	AdminName  string
	AdminEmail string
	Password   string
	UserAgent  string
	IP         string
}

func (in SignupInput) validate() error {
	orgName := strings.TrimSpace(in.OrgName)
	if orgName == "" {
		return ValidationError{"organization name is required"}
	}
	if len(orgName) > maxOrgNameLen {
		return ValidationError{fmt.Sprintf("organization name must be %d characters or fewer", maxOrgNameLen)}
	}
	adminName := strings.TrimSpace(in.AdminName)
	if adminName == "" {
		return ValidationError{"your name is required"}
	}
	if len(adminName) > maxPersonNameLen {
		return ValidationError{fmt.Sprintf("your name must be %d characters or fewer", maxPersonNameLen)}
	}
	if !emailshape.LooksLikeEmail(in.AdminEmail) {
		return ValidationError{"a valid email is required"}
	}
	if len(strings.TrimSpace(in.AdminEmail)) > maxEmailLen {
		return ValidationError{fmt.Sprintf("email must be %d characters or fewer", maxEmailLen)}
	}
	if len(in.Password) < 8 {
		return ValidationError{"password must be at least 8 characters"}
	}
	if len(in.Password) > maxPasswordLen {
		return ValidationError{fmt.Sprintf("password must be %d characters or fewer", maxPasswordLen)}
	}
	return nil
}

// Signup creates a new organization and its first (admin) user, and starts
// a session for them, all in a single transaction — a partially-created
// org with no admin user should never be observable.
func Signup(ctx context.Context, pool *pgxpool.Pool, sessions *session.Store, in SignupInput) (session.Identity, error) {
	if err := in.validate(); err != nil {
		return session.Identity{}, err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return session.Identity{}, err
	}

	orgSlug, err := uniqueOrgSlug(ctx, sqlc.New(pool), slug.SlugifyOr(in.OrgName, "org"))
	if err != nil {
		return session.Identity{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return session.Identity{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	q := sqlc.New(tx)

	orgID := id.New()
	orgRow, err := q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		ID:       db.MustUUID(orgID),
		Name:     strings.TrimSpace(in.OrgName),
		Slug:     orgSlug,
		Timezone: "UTC",
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			return session.Identity{}, ErrSlugTaken
		}
		return session.Identity{}, err
	}

	userID := id.New()
	userRow, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		ID:           db.MustUUID(userID),
		OrgID:        orgRow.ID,
		Email:        strings.TrimSpace(in.AdminEmail),
		Name:         strings.TrimSpace(in.AdminName),
		PasswordHash: hash,
		Role:         string(tenant.RoleAdmin),
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			return session.Identity{}, ErrEmailTaken
		}
		return session.Identity{}, err
	}

	ident, err := sessions.WithTx(tx).Create(ctx, userID, orgID, in.UserAgent, in.IP)
	if err != nil {
		return session.Identity{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return session.Identity{}, err
	}

	ident.Email = userRow.Email
	ident.Name = userRow.Name
	ident.Role = tenant.RoleAdmin
	return ident, nil
}

// uniqueOrgSlug finds an available slug starting from base, trying
// "base-2", "base-3", ... on collision. A small residual TOCTOU race is
// possible (caught by the DB unique constraint and surfaced as
// ErrSlugTaken) but acceptable for the low-frequency signup path.
func uniqueOrgSlug(ctx context.Context, q *sqlc.Queries, base string) (string, error) {
	candidate := base
	for i := 1; i <= 25; i++ {
		_, err := q.GetOrganizationBySlug(ctx, candidate)
		if errors.Is(err, pgx.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, i+1)
	}
	return "", ErrSlugTaken
}

// Package session manages DB-backed login sessions: opaque tokens stored
// in the sessions table, looked up on every authenticated request. There
// is no JWT and no signed-cookie state — the cookie holds only a random
// token, so revoking a session (logout, admin action) is a single row
// delete that takes effect immediately, everywhere.
package session

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// TTL is how long a session stays valid after creation without further
// activity extending it. 30 days matches typical "stay signed in" UX for a
// business dashboard.
const TTL = 30 * 24 * time.Hour

var ErrNotFound = errors.New("session: not found or expired")

// Identity is what a valid session resolves to: enough to populate the
// request's tenant context and to set the CSRF token expected on mutations.
type Identity struct {
	Token     string
	CSRFToken string
	UserID    string
	OrgID     string
	Email     string
	Name      string
	Role      tenant.Role
	ExpiresAt time.Time
}

type Store struct {
	q     *sqlc.Queries
	clock clock.Clock
}

func NewStore(dbtx sqlc.DBTX, c clock.Clock) *Store {
	return &Store{q: sqlc.New(dbtx), clock: c}
}

// WithTx returns a Store whose queries run inside tx, so session creation
// can be part of a larger atomic operation (see auth.Signup).
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), clock: s.clock}
}

func (s *Store) Create(ctx context.Context, userID, orgID, userAgent, ip string) (Identity, error) {
	token, err := newToken()
	if err != nil {
		return Identity{}, err
	}
	csrf, err := newToken()
	if err != nil {
		return Identity{}, err
	}
	expiresAt := s.clock.Now().Add(TTL)

	row, err := s.q.CreateSession(ctx, sqlc.CreateSessionParams{
		Token:     token,
		UserID:    db.MustUUID(userID),
		OrgID:     db.MustUUID(orgID),
		CsrfToken: csrf,
		ExpiresAt: db.Timestamptz(expiresAt),
		UserAgent: db.Text(userAgent),
		Ip:        db.Text(ip),
	})
	if err != nil {
		return Identity{}, err
	}

	return Identity{
		Token:     row.Token,
		CSRFToken: row.CsrfToken,
		UserID:    userID,
		OrgID:     orgID,
		ExpiresAt: db.Time(row.ExpiresAt),
	}, nil
}

// Get resolves a session token to the user/org identity it authenticates.
// It returns ErrNotFound both when the token doesn't exist and when it has
// expired or the user has been disabled — callers should treat all three
// identically (redirect to login), not distinguish them.
func (s *Store) Get(ctx context.Context, token string) (Identity, error) {
	row, err := s.q.GetSessionWithUser(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Identity{}, ErrNotFound
		}
		return Identity{}, err
	}

	if expired(s.clock.Now(), db.Time(row.SessionExpiresAt)) {
		return Identity{}, ErrNotFound
	}
	if row.UserDisabledAt.Valid {
		return Identity{}, ErrNotFound
	}

	return Identity{
		Token:     row.SessionToken,
		CSRFToken: row.SessionCsrfToken,
		UserID:    db.UUIDString(row.UserID),
		OrgID:     db.UUIDString(row.OrgID),
		Email:     row.UserEmail,
		Name:      row.UserName,
		Role:      tenant.Role(row.UserRole),
		ExpiresAt: db.Time(row.SessionExpiresAt),
	}, nil
}

// expired reports whether expiresAt has strictly passed as of now; a
// session is still valid at the exact expiry instant.
func expired(now, expiresAt time.Time) bool {
	return now.After(expiresAt)
}

func (s *Store) Delete(ctx context.Context, token string) error {
	return s.q.DeleteSession(ctx, token)
}

func (s *Store) Touch(ctx context.Context, token string) error {
	return s.q.TouchSession(ctx, token)
}

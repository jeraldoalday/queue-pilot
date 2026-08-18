// Package queue manages walk-in queues: the named lines an org's customers
// join without an appointment, the tickets they hold, and the lifecycle
// staff move those tickets through.
//
// Everything here is org-scoped the same way the rest of the app is: orgID
// comes from the caller's authenticated tenant context (staff console) or
// from the org slug resolved out of a public URL, never from a
// client-supplied field.
package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/platform/slug"
)

// ErrSlugTaken means slug disambiguation gave up after too many
// collisions, which in practice means the org has an implausible number of
// queues named the same thing.
var ErrSlugTaken = errors.New("queue: a queue with a conflicting slug already exists")

// ErrQueueClosed means the queue exists but is not accepting walk-ins.
// Callers should show this to customers as "this line is closed" rather
// than as a missing page — the queue may reopen tomorrow.
var ErrQueueClosed = errors.New("queue: this queue is not currently accepting tickets")

// ValidationError wraps a message that is always safe to show a user
// directly. See auth.ValidationError for the same pattern.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

// maxNameLen bounds a queue name so a pasted essay can't break the board
// layout or the ticket display. It is deliberately generous.
const maxNameLen = 80

type Input struct {
	Name   string
	Active bool
}

func (in Input) validate() error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return ValidationError{"queue name is required"}
	}
	if len(name) > maxNameLen {
		return ValidationError{fmt.Sprintf("queue name must be %d characters or fewer", maxNameLen)}
	}
	return nil
}

// Create adds a queue to orgID. New queues start active: an admin creating
// a line almost always wants it open, and closing it is one click away.
func Create(ctx context.Context, pool *pgxpool.Pool, orgID string, in Input) (sqlc.Queue, error) {
	if err := in.validate(); err != nil {
		return sqlc.Queue{}, err
	}

	name := strings.TrimSpace(in.Name)
	q := sqlc.New(pool)

	queueSlug, err := uniqueQueueSlug(ctx, q, orgID, slug.SlugifyOr(name, "queue"))
	if err != nil {
		return sqlc.Queue{}, err
	}

	row, err := q.CreateQueue(ctx, sqlc.CreateQueueParams{
		ID:    db.MustUUID(id.New()),
		OrgID: db.MustUUID(orgID),
		Slug:  queueSlug,
		Name:  name,
	})
	if err != nil {
		// Another request may have taken the slug between the collision
		// scan and this insert; the unique index is the real guarantee.
		if db.IsUniqueViolation(err) {
			return sqlc.Queue{}, ErrSlugTaken
		}
		return sqlc.Queue{}, err
	}
	return row, nil
}

func List(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.Queue, error) {
	return sqlc.New(pool).ListQueuesByOrg(ctx, db.MustUUID(orgID))
}

func ListActive(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.Queue, error) {
	return sqlc.New(pool).ListActiveQueuesByOrg(ctx, db.MustUUID(orgID))
}

func Get(ctx context.Context, pool *pgxpool.Pool, orgID, queueID string) (sqlc.Queue, error) {
	return sqlc.New(pool).GetQueue(ctx, sqlc.GetQueueParams{
		ID:    db.MustUUID(queueID),
		OrgID: db.MustUUID(orgID),
	})
}

// GetBySlug resolves the queue segment of a public URL. It is scoped by
// orgID (itself resolved from the org slug), so one org's queue slug can
// never select another org's queue even when both use the obvious name.
func GetBySlug(ctx context.Context, pool *pgxpool.Pool, orgID, queueSlug string) (sqlc.Queue, error) {
	return sqlc.New(pool).GetQueueBySlug(ctx, sqlc.GetQueueBySlugParams{
		OrgID: db.MustUUID(orgID),
		Slug:  queueSlug,
	})
}

// Update renames a queue or opens/closes it. Like services, the slug stays
// fixed across renames so a printed QR code or a bookmarked board URL
// doesn't stop working because someone fixed a typo in the name.
func Update(ctx context.Context, pool *pgxpool.Pool, orgID, queueID string, in Input) (sqlc.Queue, error) {
	if err := in.validate(); err != nil {
		return sqlc.Queue{}, err
	}

	return sqlc.New(pool).UpdateQueue(ctx, sqlc.UpdateQueueParams{
		ID:     db.MustUUID(queueID),
		OrgID:  db.MustUUID(orgID),
		Name:   strings.TrimSpace(in.Name),
		Active: in.Active,
	})
}

// Delete removes a queue and, by ON DELETE CASCADE, its tickets and daily
// counters. Closing a queue (Active = false) is the reversible option and
// is what the UI offers first; deleting is for lines created by mistake.
func Delete(ctx context.Context, pool *pgxpool.Pool, orgID, queueID string) error {
	return sqlc.New(pool).DeleteQueue(ctx, sqlc.DeleteQueueParams{
		ID:    db.MustUUID(queueID),
		OrgID: db.MustUUID(orgID),
	})
}

// uniqueQueueSlug mirrors catalog.uniqueServiceSlug: try the base slug,
// then base-2, base-3, … until one is free within this org.
func uniqueQueueSlug(ctx context.Context, q *sqlc.Queries, orgID, base string) (string, error) {
	candidate := base
	for i := 1; i <= 25; i++ {
		_, err := q.GetQueueBySlug(ctx, sqlc.GetQueueBySlugParams{
			OrgID: db.MustUUID(orgID),
			Slug:  candidate,
		})
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

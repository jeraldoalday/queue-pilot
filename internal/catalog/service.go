// Package catalog manages an org's bookable services and which staff can
// perform them.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/platform/slug"
)

var ErrSlugTaken = errors.New("catalog: a service with a conflicting slug already exists")

// ErrServiceInUse means the service has appointments recorded against it,
// so the history holding those appointments together will not let it be
// deleted.
var ErrServiceInUse = errors.New("catalog: this service has appointments recorded against it")

// ValidationError wraps a message that is always safe to show a user
// directly. See auth.ValidationError for the same pattern.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

type ServiceInput struct {
	Name            string
	Description     string
	DurationMinutes int
	Active          bool
}

func (in ServiceInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return ValidationError{"service name is required"}
	}
	if in.DurationMinutes <= 0 {
		return ValidationError{"duration must be greater than 0 minutes"}
	}
	// duration_minutes backs an Int4 column (CreateService/UpdateService
	// narrow to int32 below). atoiOrZero parses the form field into Go's
	// int, which is 64 bits on every platform this runs on, so a value
	// too large for int32 passes the check above and then wraps —
	// possibly negative — in the narrowing cast, tripping the database's
	// own CHECK on the small values and silently saving the wrong
	// duration on the large ones. Rejecting it here, before the cast,
	// turns that into the same validation message an out-of-range value
	// already gets.
	if in.DurationMinutes > math.MaxInt32 {
		return ValidationError{"duration is too large"}
	}
	return nil
}

// CreateService adds a service to orgID, which must come from the caller's
// authenticated tenant context — never from a client-supplied field.
func CreateService(ctx context.Context, pool *pgxpool.Pool, orgID string, in ServiceInput) (sqlc.Service, error) {
	if err := in.validate(); err != nil {
		return sqlc.Service{}, err
	}

	q := sqlc.New(pool)
	svcSlug, err := uniqueServiceSlug(ctx, q, orgID, slug.SlugifyOr(in.Name, "service"))
	if err != nil {
		return sqlc.Service{}, err
	}

	row, err := q.CreateService(ctx, sqlc.CreateServiceParams{
		ID:              db.MustUUID(id.New()),
		OrgID:           db.MustUUID(orgID),
		Slug:            svcSlug,
		Name:            in.Name,
		Description:     in.Description,
		DurationMinutes: int32(in.DurationMinutes),
		PriceCents:      db.Int4(nil), // billing is out of scope for the MVP
		Active:          true,
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			return sqlc.Service{}, ErrSlugTaken
		}
		return sqlc.Service{}, err
	}
	return row, nil
}

func ListServices(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.Service, error) {
	return sqlc.New(pool).ListServicesByOrg(ctx, db.MustUUID(orgID))
}

func GetService(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID string) (sqlc.Service, error) {
	return sqlc.New(pool).GetService(ctx, sqlc.GetServiceParams{
		ID:    db.MustUUID(serviceID),
		OrgID: db.MustUUID(orgID),
	})
}

// UpdateService leaves the slug untouched even if the name changes, so a
// service's URL/identity stays stable across edits.
func UpdateService(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID string, in ServiceInput) (sqlc.Service, error) {
	if err := in.validate(); err != nil {
		return sqlc.Service{}, err
	}

	return sqlc.New(pool).UpdateService(ctx, sqlc.UpdateServiceParams{
		ID:              db.MustUUID(serviceID),
		OrgID:           db.MustUUID(orgID),
		Name:            in.Name,
		Description:     in.Description,
		DurationMinutes: int32(in.DurationMinutes),
		PriceCents:      db.Int4(nil),
		Active:          in.Active,
	})
}

// DeleteService removes a service outright.
//
// It fails with ErrServiceInUse once anything has been booked against the
// service, because appointments.service_id is ON DELETE RESTRICT: an
// appointment records what was performed, and a schedule or a report read
// six months later would otherwise refer to a service that no longer
// exists. Deactivating is the answer in that case, which is why the two
// sit next to each other in the UI.
func DeleteService(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID string) error {
	err := sqlc.New(pool).DeleteService(ctx, sqlc.DeleteServiceParams{
		ID:    db.MustUUID(serviceID),
		OrgID: db.MustUUID(orgID),
	})
	if db.IsForeignKeyViolation(err) {
		return ErrServiceInUse
	}
	return err
}

// uniqueServiceSlug mirrors auth.uniqueOrgSlug's collision strategy,
// scoped to a single org's services instead of the global org namespace.
func uniqueServiceSlug(ctx context.Context, q *sqlc.Queries, orgID, base string) (string, error) {
	candidate := base
	for i := 1; i <= 25; i++ {
		_, err := q.GetServiceBySlug(ctx, sqlc.GetServiceBySlugParams{
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

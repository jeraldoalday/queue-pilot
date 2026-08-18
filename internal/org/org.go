// Package org resolves organization-level settings that other domains
// need, most importantly the timezone every date and time in the app is
// interpreted and displayed in.
package org

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// Location returns the org's timezone as a *time.Location.
//
// Every appointment time in QueuePilot is anchored to its org's timezone,
// not the server's: a clinic that opens at 09:00 means 09:00 where the
// clinic is, regardless of where the app happens to be deployed. Slot
// generation and display must both go through here, or a slot booked as
// "09:00" will render as some other hour.
//
// An unparseable stored timezone falls back to UTC rather than failing the
// request — a wrong-but-consistent zone degrades better than a 500.
func Location(ctx context.Context, pool *pgxpool.Pool, orgID string) (*time.Location, error) {
	o, err := sqlc.New(pool).GetOrganizationByID(ctx, db.MustUUID(orgID))
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// BySlug resolves the org segment of a public URL. Public pages have no
// session to establish a tenant, so this is where their org comes from —
// and, exactly like the authenticated path, every subsequent lookup is
// scoped by the ID it returns rather than by anything else in the URL.
func BySlug(ctx context.Context, pool *pgxpool.Pool, orgSlug string) (sqlc.Organization, error) {
	return sqlc.New(pool).GetOrganizationBySlug(ctx, orgSlug)
}

// LocationOf resolves an already-loaded org's timezone, so a caller that
// has the row doesn't re-query for it. Same fallback as Location: an
// unparseable zone degrades to UTC rather than failing the request.
func LocationOf(o sqlc.Organization) *time.Location {
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

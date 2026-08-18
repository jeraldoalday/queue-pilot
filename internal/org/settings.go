package org

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// ValidationError wraps a message that is always safe to show a user
// directly, matching the pattern the other domains use.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

// maxNameLen bounds an organization name. It appears on the public board
// above the now-serving number, so this is a layout bound as much as an
// abuse one.
const maxNameLen = 100

// Settings is what an admin may change about their organization after
// signup. The slug is absent on purpose: it is printed on QR codes stuck
// to a counter, and fixing a typo in a name must not break every board
// URL a business has handed out.
type Settings struct {
	Name     string
	Timezone string
}

func (s Settings) validate() error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return ValidationError{"organisation name is required"}
	}
	if len(name) > maxNameLen {
		return ValidationError{fmt.Sprintf("organisation name must be %d characters or fewer", maxNameLen)}
	}

	tz := strings.TrimSpace(s.Timezone)
	if tz == "" {
		return ValidationError{"timezone is required"}
	}
	// The zone is validated here rather than trusted, because every
	// service day in the app is derived from it. A stored zone the runtime
	// cannot load silently falls back to UTC (see LocationOf), which would
	// turn a typo into a queue that rolls over its ticket numbers at the
	// wrong hour with nothing anywhere saying why.
	if _, err := time.LoadLocation(tz); err != nil {
		return ValidationError{fmt.Sprintf("%q is not a timezone name — use a zone like Europe/London or Asia/Manila", tz)}
	}
	// "Local" loads successfully but means the server's zone, which is not
	// a property of the business and changes if the app is redeployed
	// somewhere else.
	if tz == "Local" {
		return ValidationError{`"Local" means the server's own timezone, which is not a property of your business — name a zone like Europe/London`}
	}

	return nil
}

// Update applies an admin's changes to their own organization. orgID comes
// from the caller's authenticated tenant context, never from the form, so
// there is no field an admin could edit to reach another org's row.
func Update(ctx context.Context, pool *pgxpool.Pool, orgID string, in Settings) (sqlc.Organization, error) {
	if err := in.validate(); err != nil {
		return sqlc.Organization{}, err
	}

	return sqlc.New(pool).UpdateOrganization(ctx, sqlc.UpdateOrganizationParams{
		ID:       db.MustUUID(orgID),
		Name:     strings.TrimSpace(in.Name),
		Timezone: strings.TrimSpace(in.Timezone),
	})
}

// Get reads one organization by ID.
func Get(ctx context.Context, pool *pgxpool.Pool, orgID string) (sqlc.Organization, error) {
	return sqlc.New(pool).GetOrganizationByID(ctx, db.MustUUID(orgID))
}

// CommonTimezones is the shortlist the settings form offers as
// suggestions. It is a convenience, not a constraint: the field accepts
// any zone the runtime knows, because the full IANA list is a thousand
// entries and a business outside this list must still be able to say
// where it is.
//
// The list is checked against the runtime at startup of the settings page
// rather than assumed — a zone dropped from the tzdata the binary was
// built against should disappear from the suggestions rather than be
// offered and then rejected on submit.
func CommonTimezones() []string {
	candidates := []string{
		"UTC",
		"Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin",
		"Europe/Madrid", "Europe/Warsaw", "Europe/Athens", "Europe/Lisbon",
		"America/New_York", "America/Chicago", "America/Denver",
		"America/Los_Angeles", "America/Toronto", "America/Vancouver",
		"America/Mexico_City", "America/Bogota", "America/Sao_Paulo",
		"Africa/Lagos", "Africa/Nairobi", "Africa/Johannesburg", "Africa/Cairo",
		"Asia/Jerusalem", "Asia/Dubai", "Asia/Karachi", "Asia/Kolkata",
		"Asia/Dhaka", "Asia/Bangkok", "Asia/Singapore", "Asia/Manila",
		"Asia/Hong_Kong", "Asia/Shanghai", "Asia/Tokyo", "Asia/Seoul",
		"Australia/Perth", "Australia/Sydney", "Pacific/Auckland",
	}

	known := make([]string, 0, len(candidates))
	for _, tz := range candidates {
		if _, err := time.LoadLocation(tz); err == nil {
			known = append(known, tz)
		}
	}
	sort.Strings(known)
	return known
}

// CurrentOffset renders what a zone means right now, e.g. "+01:00".
//
// The settings page shows it beside the field because "Europe/London" is
// not something most people can check at a glance, whereas an offset next
// to a clock they can see is. It is the current offset rather than the
// standard one, so a business in summer reads the hour it is actually
// keeping.
func CurrentOffset(tz string, now time.Time) (string, bool) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", false
	}

	_, seconds := now.In(loc).Zone()
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, (seconds%3600)/60), true
}

// IsValidationError reports whether err is a message safe to show a user.
func IsValidationError(err error) bool {
	var verr ValidationError
	return errors.As(err, &verr)
}

// describeZone renders a zone as something a person can check: the local
// time there right now, and the offset that produces it. It reports
// ok=false for a zone the runtime cannot load, which the form turns into
// guidance rather than a blank line.
func describeZone(tz string, now time.Time) (local, offset string, ok bool) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", "", false
	}
	off, ok := CurrentOffset(tz, now)
	if !ok {
		return "", "", false
	}
	return now.In(loc).Format("15:04 on Monday 2 January"), off, true
}

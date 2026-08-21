package db

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The three violation checks are how the app tells "your data says no"
// apart from "the database says no", and every friendly error message
// downstream is built on getting that right. They are also the sort of
// check that keeps working by accident: a mistyped code would simply
// classify everything as unexpected, which looks like nothing being wrong
// until an admin is shown a 500 for a duplicate name.
func TestViolationChecksMatchTheirOwnCodeOnly(t *testing.T) {
	checks := map[string]struct {
		fn   func(error) bool
		code string
	}{
		"unique":     {IsUniqueViolation, "23505"},
		"exclusion":  {IsExclusionViolation, "23P01"},
		"foreignKey": {IsForeignKeyViolation, "23503"},
	}

	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if !check.fn(&pgconn.PgError{Code: check.code}) {
				t.Errorf("Is%sViolation did not recognise code %s", name, check.code)
			}
			// Wrapped, because that is how it arrives in practice: a
			// domain function returns fmt.Errorf("...: %w", err).
			if !check.fn(fmt.Errorf("creating a row: %w", &pgconn.PgError{Code: check.code})) {
				t.Errorf("Is%sViolation did not see through a wrapped error", name)
			}
			for other, o := range checks {
				if other == name {
					continue
				}
				if check.fn(&pgconn.PgError{Code: o.code}) {
					t.Errorf("Is%sViolation also matched %s's code %s", name, other, o.code)
				}
			}
			if check.fn(errors.New("connection refused")) {
				t.Errorf("Is%sViolation matched an ordinary error", name)
			}
			if check.fn(nil) {
				t.Errorf("Is%sViolation matched nil", name)
			}
		})
	}
}

// A service day is a date in the org's timezone, and it travels to
// Postgres as a bare DATE and back. The round trip has to land on the same
// calendar day for a business several hours from UTC, or a ticket issued
// in the evening in Auckland belongs to the wrong day's numbering.
func TestDateRoundTripKeepsTheOrgsCalendarDay(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}

	// 23:30 local, which is already the following day in UTC.
	evening := time.Date(2026, 6, 15, 23, 30, 0, 0, auckland)

	stored := Date(evening)
	if got := stored.Time.Format("2006-01-02"); got != "2026-06-15" {
		t.Fatalf("Date() stored %s, want the org's own calendar day 2026-06-15", got)
	}

	back := DateTime(stored, auckland)
	if y, m, d := back.Date(); y != 2026 || m != time.June || d != 15 {
		t.Errorf("DateTime() returned %v, want 2026-06-15 in Auckland", back)
	}
	if back.Location() != auckland {
		t.Errorf("DateTime() returned a time in %v, want the org's zone", back.Location())
	}
	if h, m, s := back.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("DateTime() returned %v, want midnight — a service day starts at 00:00 local", back)
	}
}

func TestTextRoundTrip(t *testing.T) {
	// An empty optional field is NULL rather than an empty string, so a
	// missing phone number is missing rather than blank.
	if got := Text(""); got.Valid {
		t.Error("Text(\"\") produced a non-NULL value")
	}
	if got := Text("+44 7700 900000"); !got.Valid || TextString(got) != "+44 7700 900000" {
		t.Errorf("Text round trip returned %+v", got)
	}
}

func TestUUIDHelpersRejectWhatTheyShould(t *testing.T) {
	valid := "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	if !IsUUID(valid) {
		t.Error("IsUUID rejected a well-formed UUID")
	}
	for _, s := range []string{"", "notauuid", "3f2504e0-4f89-11d3-9a0c"} {
		if IsUUID(s) {
			t.Errorf("IsUUID accepted %q", s)
		}
		if _, err := ParseUUID(s); err == nil {
			t.Errorf("ParseUUID accepted %q", s)
		}
	}

	if got := UUIDString(MustUUID(valid)); got != valid {
		t.Errorf("UUID round trip = %q, want %q", got, valid)
	}
	// An optional foreign key with nothing in it must be NULL, not the
	// zero UUID — queue_tickets.called_by_user_id references users, and a
	// zero UUID would be a reference to a row that does not exist.
	if got := UUIDOrNil(""); got.Valid {
		t.Error("UUIDOrNil(\"\") produced a non-NULL value")
	}
}

// MustUUID panics by design: it is only ever called with IDs this app
// generated, so a malformed one is a bug rather than bad input. That is
// only safe while the boundary checks (IsUUID, ParseUUID, httpx.PathUUID)
// stand in front of it, and the panic is what keeps the distinction
// enforced rather than merely intended.
func TestMustUUIDPanicsOnAValueFromOutside(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustUUID accepted a malformed ID instead of panicking")
		}
	}()
	MustUUID("notauuid")
}

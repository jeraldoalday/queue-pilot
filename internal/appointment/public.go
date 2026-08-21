package appointment

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// MinLeadTime is how far ahead a customer booking themselves must book.
//
// It exists for two different reasons that happen to want the same rule.
// The obvious one is that a slot beginning in four minutes is not a
// booking, it is an ambush: nobody at the counter finds out in time. The
// less obvious one is that a booking page left open in a browser goes
// stale — the slot list it is showing was true when it rendered, and the
// window closes on its own while the customer fills in their name.
const MinLeadTime = 15 * time.Minute

// MaxBookAhead bounds how far into the future the public page will look.
//
// A recurring weekly schedule generates slots forever, so without a bound
// the date picker would happily offer next August, against availability
// nobody has thought about and staff who may not still work there. Sixty
// days is long enough for the appointments people actually plan and short
// enough that the schedule behind them is real.
const MaxBookAhead = 60 * 24 * time.Hour

// ErrTooSoon means the requested start is inside MinLeadTime — nearly
// always a form that has been sitting open while the slot passed.
var ErrTooSoon = errors.New("appointment: that time is too soon to book online")

// ErrTooFarAhead means the requested start is beyond MaxBookAhead.
var ErrTooFarAhead = errors.New("appointment: that date is further ahead than online booking goes")

// ErrSlotNotOffered means the requested staff member and start time are
// not among the slots the page would have offered. It covers a stale form
// whose slot was taken, and a hand-edited one asking for a time outside
// anyone's working hours.
var ErrSlotNotOffered = errors.New("appointment: that time is not available to book")

// ErrTooLateToCancel means the appointment has already started, so there
// is nothing left for a customer to release.
var ErrTooLateToCancel = errors.New("appointment: that appointment has already started")

// OpenSlot is one bookable time on the public page, carrying the staff
// member who would take it.
//
// The customer is not asked to choose a person: on the public side the
// question is "when can I come in", and the app answers it by assigning
// somebody who performs the service and is free then. Which of two
// equally free colleagues gets it is the app's business, not the
// customer's, and asking would turn a two-click booking into a directory.
type OpenSlot struct {
	Start       time.Time
	End         time.Time
	StaffUserID string
	StaffName   string
}

// StaffSlots pairs one staff member with the slots they can take, which is
// what MergeStaffSlots folds into a single list.
type StaffSlots struct {
	StaffUserID string
	StaffName   string
	Slots       []availability.Slot
}

// PublicBookInput is a booking request from an unauthenticated customer.
// It carries no org: the tenant comes from the URL's org slug, resolved by
// the handler, exactly as it does on the public queue pages.
type PublicBookInput struct {
	ServiceID     string
	StaffUserID   string
	StartsAt      time.Time
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
}

// MergeStaffSlots folds each staff member's free times into the single
// list a customer sees, keeping only slots inside [notBefore, notAfter].
//
// Two colleagues free at 10:00 is one 10:00 on the page, not two. A
// customer choosing between two identical times is being asked a question
// they have no way to answer, and a page that lists every counter's
// availability separately gets longer the better staffed the business is.
//
// The tie is broken by staff name so the assignment is deterministic:
// re-rendering the same page must not silently reassign a slot to a
// different person between the moment it is displayed and the moment it is
// submitted, or the confirmation would name someone the customer never
// saw. Callers pass their per-staff lists in whatever order the database
// returned them; the ordering that matters is imposed here.
func MergeStaffSlots(perStaff []StaffSlots, notBefore, notAfter time.Time) []OpenSlot {
	ordered := make([]StaffSlots, len(perStaff))
	copy(ordered, perStaff)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StaffName != ordered[j].StaffName {
			return ordered[i].StaffName < ordered[j].StaffName
		}
		return ordered[i].StaffUserID < ordered[j].StaffUserID
	})

	byStart := make(map[time.Time]OpenSlot)
	for _, staff := range ordered {
		for _, slot := range staff.Slots {
			if slot.Start.Before(notBefore) || slot.Start.After(notAfter) {
				continue
			}
			if _, taken := byStart[slot.Start]; taken {
				continue
			}
			byStart[slot.Start] = OpenSlot{
				Start:       slot.Start,
				End:         slot.End,
				StaffUserID: staff.StaffUserID,
				StaffName:   staff.StaffName,
			}
		}
	}

	out := make([]OpenSlot, 0, len(byStart))
	for _, s := range byStart {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// BookingWindow returns the earliest and latest start a public booking may
// have, given the current instant. Both ends are needed in three places —
// generating slots, validating a submission, and bounding the date
// picker — and they must agree, or the page offers times the booking
// endpoint then refuses.
func BookingWindow(now time.Time) (earliest, latest time.Time) {
	return now.Add(MinLeadTime), now.Add(MaxBookAhead)
}

// CheckBookingWindow reports whether a requested start is inside the
// window, naming which end it fell outside so the customer is told
// something more useful than "no".
func CheckBookingWindow(start, now time.Time) error {
	earliest, latest := BookingWindow(now)
	if start.Before(earliest) {
		return ErrTooSoon
	}
	if start.After(latest) {
		return ErrTooFarAhead
	}
	return nil
}

// PublicSlots returns the times a customer may book for one service on one
// date, with the staff member who would take each.
//
// It is built from the same AvailableSlots the admin booking form uses, so
// the two cannot disagree about who is free — the public page simply asks
// it once per assigned staff member and merges the answers.
//
// `date` must already be resolved in the org's timezone, and `now` is
// passed rather than read so the boundary behaviour is testable.
func PublicSlots(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID string, date, now time.Time) ([]OpenSlot, error) {
	if !db.IsUUID(serviceID) {
		return nil, ValidationError{"please choose a service"}
	}

	q := sqlc.New(pool)

	svc, err := q.GetService(ctx, sqlc.GetServiceParams{
		ID:    db.MustUUID(serviceID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return nil, err
	}
	// An inactive service is not on the public list, and must not be
	// bookable by anyone who kept its URL.
	if !svc.Active {
		return nil, pgx.ErrNoRows
	}

	staff, err := q.ListActiveStaffForService(ctx, sqlc.ListActiveStaffForServiceParams{
		ServiceID: svc.ID,
		OrgID:     db.MustUUID(orgID),
	})
	if err != nil {
		return nil, err
	}

	perStaff := make([]StaffSlots, 0, len(staff))
	for _, member := range staff {
		staffID := db.UUIDString(member.ID)
		slots, err := AvailableSlots(ctx, pool, orgID, staffID, serviceID, date)
		if err != nil {
			return nil, err
		}
		perStaff = append(perStaff, StaffSlots{
			StaffUserID: staffID,
			StaffName:   member.Name,
			Slots:       slots,
		})
	}

	earliest, latest := BookingWindow(now)
	return MergeStaffSlots(perStaff, earliest, latest), nil
}

// BookPublic creates an appointment from the public page.
//
// It re-derives the day's slots and requires the submitted one to be among
// them, rather than trusting the form. The form is the only thing standing
// between a customer and the appointments table, and it is entirely under
// the customer's control: without this check, an edited request could book
// a staff member at three in the morning, book a service they do not
// perform, or book a slot that was offered an hour ago and taken since.
//
// The actual write goes through Book, so the exclusion constraint, the
// staff-assignment check and the overlap handling are the same ones the
// admin path gets. What is added here is the public-only part: the
// booking window, and the rule that a customer may only take a time the
// page would have shown them.
func BookPublic(ctx context.Context, pool *pgxpool.Pool, orgID string, in PublicBookInput, now time.Time) (sqlc.Appointment, error) {
	if strings.TrimSpace(in.CustomerName) == "" {
		return sqlc.Appointment{}, ValidationError{"please enter your name"}
	}
	if !looksLikeEmail(in.CustomerEmail) {
		return sqlc.Appointment{}, ValidationError{"please enter an email we can send the confirmation to"}
	}
	if in.StartsAt.IsZero() {
		return sqlc.Appointment{}, ValidationError{"please choose a time"}
	}
	if err := CheckBookingWindow(in.StartsAt, now); err != nil {
		return sqlc.Appointment{}, err
	}

	offered, err := PublicSlots(ctx, pool, orgID, in.ServiceID, in.StartsAt, now)
	if err != nil {
		return sqlc.Appointment{}, err
	}

	slot, ok := findSlot(offered, in.StartsAt, in.StaffUserID)
	if !ok {
		return sqlc.Appointment{}, ErrSlotNotOffered
	}

	return Book(ctx, pool, orgID, BookInput{
		ServiceID:     in.ServiceID,
		StaffUserID:   slot.StaffUserID,
		CustomerName:  strings.TrimSpace(in.CustomerName),
		CustomerEmail: strings.TrimSpace(in.CustomerEmail),
		CustomerPhone: strings.TrimSpace(in.CustomerPhone),
		StartsAt:      slot.Start,
	})
}

// findSlot locates the submitted start among the offered slots.
//
// The staff member is matched when the form named one, so a booking
// confirms with the person the page showed. When it named nobody — or
// named somebody who is no longer free at that time — the slot's own
// assignment is used instead: the customer asked for a time, and the time
// is still available.
func findSlot(offered []OpenSlot, start time.Time, staffUserID string) (OpenSlot, bool) {
	var atTime *OpenSlot
	for i, s := range offered {
		if !s.Start.Equal(start) {
			continue
		}
		if s.StaffUserID == staffUserID {
			return s, true
		}
		if atTime == nil {
			atTime = &offered[i]
		}
	}
	if atTime != nil {
		return *atTime, true
	}
	return OpenSlot{}, false
}

// GetPublicAppointment resolves the appointment behind a customer's
// confirmation link.
//
// There is no session to scope by, so it scopes by the org resolved from
// the URL and relies on the ID being an unguessable UUID — the same shape
// the public queue ticket page uses. A malformed ID reads as not found
// rather than reaching db.MustUUID, which panics by design.
func GetPublicAppointment(ctx context.Context, pool *pgxpool.Pool, orgID, appointmentID string) (sqlc.GetAppointmentWithNamesRow, error) {
	id, err := db.ParseUUID(appointmentID)
	if err != nil {
		return sqlc.GetAppointmentWithNamesRow{}, pgx.ErrNoRows
	}
	return sqlc.New(pool).GetAppointmentWithNames(ctx, sqlc.GetAppointmentWithNamesParams{
		ID:    id,
		OrgID: db.MustUUID(orgID),
	})
}

// CancelPublic lets a customer release their own booking from the
// confirmation page.
//
// This is the counterpart of the walk-in queue's "leave the line", and it
// exists for the same reason: a customer who simply doesn't come is
// recorded as a no-show, and the slot stays blocked until someone notices.
// Cancelling frees the time for the next person to book, which is the
// whole point of holding a schedule.
//
// It refuses once the appointment has started. By then the staff member
// has kept the time, and a cancellation would rewrite a no-show the
// business needs to be able to see.
func CancelPublic(ctx context.Context, pool *pgxpool.Pool, orgID, appointmentID string, now time.Time) (sqlc.Appointment, error) {
	if !db.IsUUID(appointmentID) {
		return sqlc.Appointment{}, pgx.ErrNoRows
	}

	current, err := sqlc.New(pool).GetAppointment(ctx, sqlc.GetAppointmentParams{
		ID:    db.MustUUID(appointmentID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return sqlc.Appointment{}, err
	}
	if !db.Time(current.StartsAt).After(now) {
		return sqlc.Appointment{}, ErrTooLateToCancel
	}

	return SetStatus(ctx, pool, orgID, appointmentID, StatusCancelled)
}

// ListBookableServices returns the services the public page offers.
func ListBookableServices(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.Service, error) {
	return sqlc.New(pool).ListBookableServicesByOrg(ctx, db.MustUUID(orgID))
}

// looksLikeEmail is the same deliberately loose check auth uses: enough to
// catch a typo'd or empty field, not an attempt to validate deliverability,
// which only sending mail can do.
func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n")
}

package appointment

import (
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/icalendar"
	"github.com/jeraldoalday/queuepilot/internal/platform/slug"
)

// This file turns a booking into the calendar entry a customer downloads.
// It is pure: it takes rows that are already loaded and returns a
// document, so what ends up in somebody's calendar can be asserted
// without a database — which matters more here than usual, because the
// output is a file that leaves the app and is read by software nobody
// here controls.

// calendarUIDSuffix qualifies an event's UID.
//
// A UID has to be globally unique, and an appointment's UUID already is,
// but a bare UUID in somebody's calendar alongside events from a dozen
// other systems is a collision waiting for the one system that reuses
// one. The suffix also makes it obvious, to anyone reading a calendar
// file six months from now, where the event came from.
const calendarUIDSuffix = "@queuepilot"

// AppointmentCalendar renders one booking as a downloadable calendar.
//
// pageURL is the customer's own confirmation page, absolute, because a
// calendar entry is read outside the browser that produced it and a
// relative path there resolves to nothing. It is the page they cancel
// from, so it is worth carrying: an event with a link is one tap from
// releasing the slot, and a slot released is the whole reason cancelling
// is offered.
func AppointmentCalendar(o sqlc.Organization, appt sqlc.GetAppointmentWithNamesRow, pageURL string) icalendar.Calendar {
	status, method, sequence := calendarState(Status(appt.Status))

	return icalendar.Calendar{
		Name:   o.Name,
		Method: method,
		Events: []icalendar.Event{{
			UID:   db.UUIDString(appt.ID) + calendarUIDSuffix,
			Start: db.Time(appt.StartsAt),
			End:   db.Time(appt.EndsAt),
			// The stamp is the booking's own last change rather than the
			// current instant, so downloading the same unchanged booking
			// twice produces the same file. A DTSTAMP that moved on every
			// request would make every download look like an update to a
			// client comparing two copies.
			Stamp:       db.Time(appt.UpdatedAt),
			Created:     db.Time(appt.CreatedAt),
			Summary:     calendarSummary(o, appt),
			Description: calendarDescription(appt, pageURL),
			// There is no address on an organization yet, so the location
			// is the business's name. It is what a customer needs to
			// recognise the entry, and it is better than an empty field
			// that reads as "location unknown".
			Location:      o.Name,
			URL:           pageURL,
			Status:        status,
			Sequence:      sequence,
			OrganizerName: o.Name,
		}},
	}
}

// calendarState maps a booking's status onto the three things a calendar
// client needs to decide what to do with the file.
//
// The sequence is the subtle one. A client that already holds an event
// ignores a new copy whose SEQUENCE has not increased, so a cancellation
// issued at 0 against a confirmation also at 0 may simply be dropped and
// the customer's calendar goes on showing an appointment that no longer
// exists. Every state past "booked" is therefore issued at 1: it is a
// later word on the same event, and it has to outrank the first.
func calendarState(s Status) (icalendar.Status, string, int) {
	switch s {
	case StatusCancelled, StatusNoShow:
		// A missed appointment is cancelled as far as a calendar is
		// concerned. The business's records distinguish the two; the
		// customer's phone has no use for the distinction, and leaving a
		// no-show sitting in it as a confirmed future event would be
		// actively wrong.
		return icalendar.StatusCancelled, icalendar.MethodCancel, 1
	case StatusCompleted:
		return icalendar.StatusConfirmed, icalendar.MethodPublish, 1
	default:
		return icalendar.StatusConfirmed, icalendar.MethodPublish, 0
	}
}

// calendarSummary is the one line a calendar shows in a day view, so it
// has to answer "what is this" without being opened: the service, and who
// it is with.
func calendarSummary(o sqlc.Organization, appt sqlc.GetAppointmentWithNamesRow) string {
	return appt.ServiceName + " at " + o.Name
}

// calendarDescription is what the entry says when it is opened.
//
// The link is repeated in the body as well as in URL because clients are
// inconsistent about surfacing the URL property, and a customer looking
// at the entry a week later needs the way back to it more than they need
// anything else here.
func calendarDescription(appt sqlc.GetAppointmentWithNamesRow, pageURL string) string {
	body := appt.ServiceName + " with " + appt.StaffName + "."
	if pageURL != "" {
		body += "\n\nView or cancel this booking: " + pageURL
	}
	return body
}

// CalendarFilename names the downloaded file.
//
// Browsers save it under whatever this says, and it lands in a folder
// with everything else the customer has ever downloaded — so it names the
// business and the day rather than being another appointment.ics. The
// date is the appointment's own, in the org's timezone, because that is
// the day the customer thinks of it as.
func CalendarFilename(o sqlc.Organization, appt sqlc.GetAppointmentWithNamesRow, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	name := slug.SlugifyOr(o.Name, "appointment")
	return name + "-" + db.Time(appt.StartsAt).In(loc).Format("2006-01-02") + ".ics"
}

// Package icalendar writes iCalendar (RFC 5545) documents.
//
// It exists so QueuePilot can hand a customer their booking as a file
// their own calendar understands. A confirmation page is only useful
// while the tab is open; the appointment matters at 10:00 next Tuesday,
// which is a question people ask their phone rather than a web page. One
// .ics download puts the time somewhere it will be answered.
//
// The subset written here is deliberately small — VCALENDAR, VEVENT, and
// the handful of properties an event needs to be recognised, updated and
// cancelled. It is not a general iCalendar library, and it does not parse.
// What it does guarantee is the three things importers are unforgiving
// about:
//
//   - CRLF line endings, which the specification requires and which some
//     parsers enforce strictly enough to reject an otherwise valid file;
//   - long lines folded at 75 octets without splitting a UTF-8 rune, so a
//     name with an accent in it does not arrive as mojibake;
//   - TEXT values escaped, so a service description containing a comma or
//     a semicolon does not silently become two properties.
//
// Every instant is written in UTC. The alternative — a local time with a
// TZID parameter — requires shipping a VTIMEZONE block describing that
// zone's daylight-saving rules, and a wrong or missing one moves the
// appointment by an hour in the customer's calendar. A UTC instant names
// the same moment everywhere and is displayed by every client in the
// reader's own zone, which is what somebody looking at their phone wants.
package icalendar

import (
	"strings"
	"time"
)

// ContentType is what an .ics response should be served as. The charset is
// spelled out because the file may carry any customer's name.
const ContentType = "text/calendar; charset=utf-8"

// Status is an event's VEVENT STATUS.
type Status string

const (
	// StatusConfirmed is a booking that stands.
	StatusConfirmed Status = "CONFIRMED"
	// StatusCancelled is one that has been called off. A calendar that
	// already holds the event removes it on seeing this, which is why a
	// cancelled booking is still worth downloading rather than being
	// refused: the file is how the customer's phone finds out.
	StatusCancelled Status = "CANCELLED"
	// StatusTentative is a booking that is not yet certain. Nothing in
	// QueuePilot issues one today; it is here because the property is a
	// closed set of three and a caller reaching for the third should not
	// have to spell it as a string.
	StatusTentative Status = "TENTATIVE"
)

// Method is the VCALENDAR METHOD, which tells a client what the file is
// for rather than what it contains.
const (
	// MethodPublish is an event being handed out, with no reply expected.
	// It is the right method for a confirmation download: the business is
	// not inviting the customer to a meeting, it is giving them a copy of
	// a time they already chose.
	MethodPublish = "PUBLISH"
	// MethodCancel withdraws an event a client may already hold.
	MethodCancel = "CANCEL"
)

// Event is one VEVENT.
//
// UID and Start are the only fields with no sensible default: an event
// without a UID cannot be updated or cancelled later, because the UID is
// the only thing tying this file to the one downloaded last week, and an
// event without a start is not an event.
type Event struct {
	// UID identifies the event across every copy of it. It must be
	// globally unique and, crucially, stable — the same appointment
	// downloaded twice has to carry the same UID, or a customer who
	// re-downloads after a change ends up with two entries instead of an
	// updated one.
	UID string

	Start time.Time
	// End may be zero, for an event with no stated duration. Most clients
	// then show it as a point in time rather than a block.
	End time.Time

	// Stamp is DTSTAMP: when this *file* was created, which is not when
	// the appointment was made. A zero value is filled in with the
	// calendar's own render time, since the property is mandatory.
	Stamp time.Time
	// Created is when the booking itself was made. Optional.
	Created time.Time

	Summary     string
	Description string
	Location    string
	// URL points back at the page the event came from — the customer's
	// own confirmation page, which is where they cancel from. Calendar
	// clients surface it as a link on the event, which is the difference
	// between "call the shop" and one tap.
	URL string

	Status Status
	// Sequence is bumped every time an event is re-issued after a change.
	// A client that already holds the event ignores an update whose
	// sequence is not higher than the one it has, so a cancellation sent
	// at sequence 0 against a confirmation also at sequence 0 may simply
	// be dropped.
	Sequence int

	OrganizerName  string
	OrganizerEmail string
}

// Calendar is a whole VCALENDAR document.
type Calendar struct {
	// ProdID identifies the software that wrote the file. It is mandatory
	// and free-form; a blank one falls back to DefaultProdID.
	ProdID string
	// Method is optional. Leaving it empty is correct for a plain
	// download, and clients treat that as PUBLISH anyway.
	Method string
	// Name is a human-readable calendar name (X-WR-CALNAME). It is not
	// standard, but every major client reads it, and it is what stops a
	// downloaded file being offered as "Untitled".
	Name   string
	Events []Event
}

// DefaultProdID is used when a Calendar names no product. The shape is the
// one RFC 5545 suggests: an FPI-ish string identifying vendor and product.
const DefaultProdID = "-//QueuePilot//QueuePilot//EN"

// maxLineOctets is the fold width from RFC 5545 section 3.1: a content
// line is at most 75 octets, excluding the line break.
const maxLineOctets = 75

// crlf is the only line break the format allows. A bare newline is a
// common source of files that work in one client and are rejected by the
// next.
const crlf = "\r\n"

// Render writes the calendar out.
//
// `now` fills in any DTSTAMP the caller left zero, and is passed rather
// than read from the clock so a handler's output is reproducible in a
// test — two renders of the same appointment should differ only when
// something about the appointment differs.
func (c Calendar) Render(now time.Time) string {
	var b strings.Builder

	prodID := c.ProdID
	if strings.TrimSpace(prodID) == "" {
		prodID = DefaultProdID
	}

	write := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(fold(name + ":" + value))
		b.WriteString(crlf)
	}

	b.WriteString("BEGIN:VCALENDAR" + crlf)
	// VERSION and PRODID are both mandatory, and VERSION is 2.0 — the
	// only version there has ever been.
	write("VERSION", "2.0")
	write("PRODID", escapeText(prodID))
	write("CALSCALE", "GREGORIAN")
	write("METHOD", c.Method)
	write("X-WR-CALNAME", escapeText(c.Name))

	for _, e := range c.Events {
		b.WriteString(e.render(now))
	}

	b.WriteString("END:VCALENDAR" + crlf)
	return b.String()
}

// Bytes is Render for handlers, which write bytes.
func (c Calendar) Bytes(now time.Time) []byte { return []byte(c.Render(now)) }

func (e Event) render(now time.Time) string {
	var b strings.Builder

	write := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(fold(name + ":" + value))
		b.WriteString(crlf)
	}

	stamp := e.Stamp
	if stamp.IsZero() {
		stamp = now
	}

	b.WriteString("BEGIN:VEVENT" + crlf)
	write("UID", escapeText(e.UID))
	write("DTSTAMP", formatUTC(stamp))
	write("DTSTART", formatUTC(e.Start))
	if !e.End.IsZero() {
		write("DTEND", formatUTC(e.End))
	}
	if !e.Created.IsZero() {
		write("CREATED", formatUTC(e.Created))
		// LAST-MODIFIED is written alongside CREATED with the stamp's
		// value: this file is the latest word on the event, and a client
		// deciding between two copies uses it.
		write("LAST-MODIFIED", formatUTC(stamp))
	}
	write("SUMMARY", escapeText(e.Summary))
	write("DESCRIPTION", escapeText(e.Description))
	write("LOCATION", escapeText(e.Location))
	// URL is not a TEXT value, so it is not escaped — a URL's own commas
	// and semicolons are part of it. It is dropped if it contains
	// anything that would break the line structure, which no real URL
	// does.
	write("URL", sanitizeLine(e.URL))
	write("STATUS", string(e.Status))
	write("SEQUENCE", itoa(max(e.Sequence, 0)))
	write("ORGANIZER", organizer(e.OrganizerName, e.OrganizerEmail))
	b.WriteString("END:VEVENT" + crlf)

	return b.String()
}

// organizer builds an ORGANIZER property, which is a URI value with an
// optional display-name parameter rather than plain text.
//
// Without an email address there is nothing to point the property at, so
// it is omitted entirely: an organizer that names somebody unreachable is
// worse than none, because a client will offer to write to it.
func organizer(name, email string) string {
	email = sanitizeLine(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	if name = strings.TrimSpace(name); name != "" {
		// A parameter value is quoted rather than escaped, so the one
		// character that cannot appear inside it is the quote itself.
		return `;CN="` + strings.ReplaceAll(sanitizeLine(name), `"`, "'") + `":mailto:` + email
	}
	return ":mailto:" + email
}

// formatUTC writes an instant in the format the spec calls a UTC
// date-time: no separators, and a trailing Z that is what makes it
// absolute rather than floating.
func formatUTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("20060102T150405Z")
}

// escapeText escapes a TEXT value per RFC 5545 section 3.3.11.
//
// The backslash has to go first: escaping it after the others would
// double the backslashes this function itself introduced.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	// Any of the three line endings a pasted value might carry becomes
	// the literal two characters an importer reads back as a newline.
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	return stripControl(s)
}

// sanitizeLine removes what would break the line structure from a value
// that is not TEXT and so cannot be escaped.
func sanitizeLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return stripControl(s)
}

// stripControl drops the remaining control characters. They are not valid
// in a content line, and a stray one is the kind of thing that makes a
// whole file fail to import for reasons no error message explains.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// fold breaks a content line at 75 octets, continuing each piece with a
// single leading space — the "line folding" of RFC 5545 section 3.1.
//
// The width is counted in octets rather than characters, because that is
// what the specification says and what strict parsers check. The break is
// only ever taken between runes: splitting a multi-byte character across
// two lines produces a file that unfolds back into a different string,
// which is how a customer named "José" becomes garbage in somebody's
// calendar.
func fold(line string) string {
	if len(line) <= maxLineOctets {
		return line
	}

	var b strings.Builder
	b.Grow(len(line) + 2*(len(line)/maxLineOctets+1))

	// The first line may use the full width; every continuation spends
	// one octet on the leading space that marks it as one.
	remaining := maxLineOctets
	for _, r := range line {
		size := runeLen(r)
		if size > remaining {
			b.WriteString(crlf)
			b.WriteByte(' ')
			remaining = maxLineOctets - 1
		}
		b.WriteRune(r)
		remaining -= size
	}
	return b.String()
}

func runeLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// itoa avoids a strconv import for the one small non-negative number this
// package formats.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

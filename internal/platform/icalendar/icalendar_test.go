package icalendar

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// unfold reverses the line folding, which is what an importer does before
// it reads anything. Tests assert against the unfolded lines so they
// describe the values the file carries rather than where the wrapping
// happened to land.
func unfold(s string) []string {
	if !strings.HasSuffix(s, crlf) {
		panic("calendar did not end with CRLF")
	}
	joined := strings.ReplaceAll(s, crlf+" ", "")
	return strings.Split(strings.TrimSuffix(joined, crlf), crlf)
}

func property(t *testing.T, out, name string) string {
	t.Helper()
	var found string
	for _, line := range unfold(out) {
		if strings.HasPrefix(line, name+":") || strings.HasPrefix(line, name+";") {
			if found != "" {
				t.Fatalf("property %s appears more than once", name)
			}
			found = strings.TrimPrefix(line, name)
		}
	}
	if found == "" {
		t.Fatalf("property %s missing from:\n%s", name, out)
	}
	return strings.TrimPrefix(found, ":")
}

func hasProperty(out, name string) bool {
	for _, line := range unfold(out) {
		if strings.HasPrefix(line, name+":") || strings.HasPrefix(line, name+";") {
			return true
		}
	}
	return false
}

var renderedAt = time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)

func sampleCalendar() Calendar {
	tokyo := time.FixedZone("JST", 9*3600)
	return Calendar{
		Name:   "Acme Dental",
		Method: MethodPublish,
		Events: []Event{{
			UID:            "appt-1@queuepilot",
			Start:          time.Date(2026, 3, 10, 10, 0, 0, 0, tokyo),
			End:            time.Date(2026, 3, 10, 10, 30, 0, 0, tokyo),
			Created:        time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC),
			Summary:        "Cleaning with Dana",
			Description:    "Booked at Acme Dental.",
			Location:       "Acme Dental",
			URL:            "https://example.test/book/acme/a/appt-1",
			Status:         StatusConfirmed,
			OrganizerName:  "Acme Dental",
			OrganizerEmail: "hello@example.test",
		}},
	}
}

func TestRenderStructure(t *testing.T) {
	out := sampleCalendar().Render(renderedAt)
	lines := unfold(out)

	if lines[0] != "BEGIN:VCALENDAR" {
		t.Errorf("first line = %q, want BEGIN:VCALENDAR", lines[0])
	}
	if lines[len(lines)-1] != "END:VCALENDAR" {
		t.Errorf("last line = %q, want END:VCALENDAR", lines[len(lines)-1])
	}
	if got := property(t, out, "VERSION"); got != "2.0" {
		t.Errorf("VERSION = %q, want 2.0", got)
	}
	if got := property(t, out, "PRODID"); got != DefaultProdID {
		t.Errorf("PRODID = %q, want the default %q", got, DefaultProdID)
	}
	if got := property(t, out, "METHOD"); got != MethodPublish {
		t.Errorf("METHOD = %q, want %q", got, MethodPublish)
	}
	if !strings.Contains(out, "BEGIN:VEVENT"+crlf) || !strings.Contains(out, "END:VEVENT"+crlf) {
		t.Errorf("event is not wrapped in BEGIN/END VEVENT:\n%s", out)
	}
}

// Every break in the file has to be CRLF. A bare newline is valid to a
// forgiving parser and a hard failure to a strict one, and the strict one
// is somebody's phone.
func TestRenderUsesCRLFThroughout(t *testing.T) {
	out := sampleCalendar().Render(renderedAt)
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
			t.Fatalf("bare newline at offset %d in:\n%q", i, out)
		}
		if out[i] == '\r' && (i+1 >= len(out) || out[i+1] != '\n') {
			t.Fatalf("bare carriage return at offset %d in:\n%q", i, out)
		}
	}
}

// The whole point of writing instants in UTC is that a booking made in one
// zone names the same moment when it is read in another.
func TestTimesAreWrittenInUTC(t *testing.T) {
	out := sampleCalendar().Render(renderedAt)

	if got := property(t, out, "DTSTART"); got != "20260310T010000Z" {
		t.Errorf("DTSTART = %q, want 10:00 JST as 01:00 UTC", got)
	}
	if got := property(t, out, "DTEND"); got != "20260310T013000Z" {
		t.Errorf("DTEND = %q, want 10:30 JST as 01:30 UTC", got)
	}
	if got := property(t, out, "CREATED"); got != "20260301T080000Z" {
		t.Errorf("CREATED = %q", got)
	}
}

func TestStampDefaultsToRenderTime(t *testing.T) {
	cal := sampleCalendar()
	if got := property(t, cal.Render(renderedAt), "DTSTAMP"); got != "20260302T093000Z" {
		t.Errorf("DTSTAMP = %q, want the render time", got)
	}

	cal.Events[0].Stamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := property(t, cal.Render(renderedAt), "DTSTAMP"); got != "20260101T000000Z" {
		t.Errorf("DTSTAMP = %q, want the explicit stamp", got)
	}
}

func TestEventWithoutEndOmitsDTEND(t *testing.T) {
	cal := sampleCalendar()
	cal.Events[0].End = time.Time{}
	if hasProperty(cal.Render(renderedAt), "DTEND") {
		t.Error("an event with no end still wrote a DTEND")
	}
}

func TestEscapeText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"nothing to escape", "Cleaning", "Cleaning"},
		{"comma", "Cleaning, with Dana", `Cleaning\, with Dana`},
		{"semicolon", "Cleaning; 30 min", `Cleaning\; 30 min`},
		{"backslash", `C:\clinic`, `C:\\clinic`},
		{"newline", "line one\nline two", `line one\nline two`},
		{"windows newline is one escape", "line one\r\nline two", `line one\nline two`},
		{"backslash before a comma is not double-escaped", `a\,b`, `a\\\,b`},
		{"a tab becomes a space", "a\tb", "a b"},
		{"other control characters are dropped", "a\x00b\x07c", "abc"},
		{"non-ascii is left alone", "José — Härkönen", "José — Härkönen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeText(tt.in); got != tt.want {
				t.Errorf("escapeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A description carrying a comma must arrive as one value. Unescaped, a
// TEXT property with a comma in it is read as a list, and the customer's
// calendar shows half the sentence.
func TestTextValuesAreEscapedInOutput(t *testing.T) {
	cal := sampleCalendar()
	cal.Events[0].Description = "Cleaning, 30 minutes; bring your card"
	out := cal.Render(renderedAt)

	if got := property(t, out, "DESCRIPTION"); got != `Cleaning\, 30 minutes\; bring your card` {
		t.Errorf("DESCRIPTION = %q", got)
	}
}

// A URL is a URI value, not TEXT: escaping its commas would change the
// address rather than protect it.
func TestURLIsNotEscaped(t *testing.T) {
	cal := sampleCalendar()
	cal.Events[0].URL = "https://example.test/book/acme?a=1,2&b=x;y"
	if got := property(t, cal.Render(renderedAt), "URL"); got != "https://example.test/book/acme?a=1,2&b=x;y" {
		t.Errorf("URL = %q, want the address unchanged", got)
	}
}

func TestFoldWrapsAtSeventyFiveOctets(t *testing.T) {
	long := "SUMMARY:" + strings.Repeat("a", 300)
	folded := fold(long)

	for i, line := range strings.Split(folded, crlf) {
		if len(line) > maxLineOctets {
			t.Errorf("line %d is %d octets, want at most %d", i, len(line), maxLineOctets)
		}
		if i > 0 && !strings.HasPrefix(line, " ") {
			t.Errorf("continuation line %d does not start with a space: %q", i, line)
		}
	}
	if got := strings.ReplaceAll(folded, crlf+" ", ""); got != long {
		t.Errorf("unfolding did not restore the line:\ngot  %q\nwant %q", got, long)
	}
}

// Folding counts octets, so a line of multi-byte characters wraps sooner
// than a line of the same number of ASCII ones — and never mid-character.
func TestFoldNeverSplitsARune(t *testing.T) {
	line := "SUMMARY:" + strings.Repeat("é", 200)
	folded := fold(line)

	for _, piece := range strings.Split(folded, crlf) {
		if len(piece) > maxLineOctets {
			t.Errorf("piece is %d octets, want at most %d", len(piece), maxLineOctets)
		}
		if !utf8.ValidString(piece) {
			t.Errorf("piece is not valid UTF-8: %q", piece)
		}
	}
	if got := strings.ReplaceAll(folded, crlf+" ", ""); got != line {
		t.Error("unfolding a multi-byte line did not restore it")
	}
}

func TestShortLineIsNotFolded(t *testing.T) {
	if got := fold("SUMMARY:Cleaning"); got != "SUMMARY:Cleaning" {
		t.Errorf("fold(short) = %q, want it untouched", got)
	}
	exact := "X:" + strings.Repeat("a", maxLineOctets-2)
	if got := fold(exact); got != exact {
		t.Errorf("a line of exactly %d octets was folded", maxLineOctets)
	}
}

func TestCancelledEvent(t *testing.T) {
	cal := sampleCalendar()
	cal.Method = MethodCancel
	cal.Events[0].Status = StatusCancelled
	cal.Events[0].Sequence = 1
	out := cal.Render(renderedAt)

	if got := property(t, out, "STATUS"); got != "CANCELLED" {
		t.Errorf("STATUS = %q", got)
	}
	if got := property(t, out, "METHOD"); got != MethodCancel {
		t.Errorf("METHOD = %q", got)
	}
	// A client that already holds the confirmation ignores an update
	// whose sequence has not moved, so the cancellation has to outrank it.
	if got := property(t, out, "SEQUENCE"); got != "1" {
		t.Errorf("SEQUENCE = %q, want 1", got)
	}
}

func TestSequenceZeroIsStillWritten(t *testing.T) {
	if got := property(t, sampleCalendar().Render(renderedAt), "SEQUENCE"); got != "0" {
		t.Errorf("SEQUENCE = %q, want 0", got)
	}
}

func TestOrganizer(t *testing.T) {
	tests := []struct {
		name  string
		cn    string
		email string
		want  string
	}{
		{"name and address", "Acme Dental", "hello@example.test", `;CN="Acme Dental":mailto:hello@example.test`},
		{"address only", "", "hello@example.test", ":mailto:hello@example.test"},
		{"no address means no organizer", "Acme Dental", "", ""},
		{"a quote in the name cannot close the parameter", `Bob "The Drill"`, "b@example.test", `;CN="Bob 'The Drill'":mailto:b@example.test`},
		{"a newline cannot inject a property", "Acme\r\nX-EVIL:1", "b@example.test", `;CN="AcmeX-EVIL:1":mailto:b@example.test`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := organizer(tt.cn, tt.email); got != tt.want {
				t.Errorf("organizer(%q, %q) = %q, want %q", tt.cn, tt.email, got, tt.want)
			}
		})
	}
}

// A value carrying a newline must not be able to end its own line and
// start a property of its own choosing — the calendar equivalent of
// header injection, reachable here because customers type their own names.
func TestValuesCannotInjectProperties(t *testing.T) {
	cal := sampleCalendar()
	cal.Events[0].Summary = "Cleaning\r\nX-INJECTED:yes"
	out := cal.Render(renderedAt)

	if hasProperty(out, "X-INJECTED") {
		t.Errorf("a summary injected a property:\n%s", out)
	}
	if got := property(t, out, "SUMMARY"); got != `Cleaning\nX-INJECTED:yes` {
		t.Errorf("SUMMARY = %q, want the injection escaped into the value", got)
	}
}

func TestCustomProdIDAndEmptyCalendar(t *testing.T) {
	cal := Calendar{ProdID: "-//Acme//Test//EN"}
	out := cal.Render(renderedAt)

	if got := property(t, out, "PRODID"); got != "-//Acme//Test//EN" {
		t.Errorf("PRODID = %q", got)
	}
	if strings.Contains(out, "VEVENT") {
		t.Error("an empty calendar wrote an event")
	}
	if hasProperty(out, "METHOD") {
		t.Error("an unset method was written anyway")
	}
}

func TestBytesMatchesRender(t *testing.T) {
	cal := sampleCalendar()
	if string(cal.Bytes(renderedAt)) != cal.Render(renderedAt) {
		t.Error("Bytes and Render disagree")
	}
}

func TestItoa(t *testing.T) {
	for _, tt := range []struct {
		in   int
		want string
	}{{0, "0"}, {1, "1"}, {9, "9"}, {10, "10"}, {2026, "2026"}} {
		if got := itoa(tt.in); got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

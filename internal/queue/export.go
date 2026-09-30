package queue

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// exportColumns is the header row, and the order every row is written in.
//
// It is a fixed list rather than something derived from the table, so
// adding a column to queue_tickets cannot silently change the shape of a
// file somebody's spreadsheet already has formulas pointing at.
var exportColumns = []string{
	"number",
	"status",
	"customer_name",
	"customer_phone",
	"issued_at",
	"called_at",
	"serving_at",
	"completed_at",
	"wait_seconds",
	"service_seconds",
}

// exportTimeLayout writes each instant with its offset, so a file opened
// somewhere else stays unambiguous about what "09:15" meant. The times
// themselves are rendered in the org's timezone, because that is the day
// the row belongs to.
const exportTimeLayout = "2006-01-02 15:04:05 -07:00"

// WriteCSV writes one service day's tickets as CSV.
//
// This exists because a queue's history is the org's own data and should
// not be trapped behind a page. Payroll wants hours worked, a manager
// wants last month in a spreadsheet, and an accountant wants something
// that opens in Excel — none of which the report page can be shaped into
// without becoming three different pages.
//
// Durations are written in seconds rather than as formatted text: a
// spreadsheet can average a number, and "6m 30s" is a string it cannot do
// anything with. Where a duration is unmeasurable — a ticket cancelled
// before it was ever called — the cell is left empty rather than filled
// with a zero, since an empty cell is excluded from an average and a zero
// silently drags it down.
func WriteCSV(w io.Writer, tickets []sqlc.QueueTicket, loc *time.Location) error {
	if loc == nil {
		loc = time.UTC
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(exportColumns); err != nil {
		return err
	}

	for _, t := range tickets {
		row := []string{
			strconv.Itoa(int(t.Number)),
			t.Status,
			csvFormulaSafe(t.CustomerName),
			csvFormulaSafe(db.TextString(t.CustomerPhone)),
			exportTime(t.CreatedAt, loc),
			exportTime(t.CalledAt, loc),
			exportTime(t.ServingAt, loc),
			exportTime(t.CompletedAt, loc),
			exportSeconds(span(t.CreatedAt, t.CalledAt)),
			exportSeconds(span(t.ServingAt, t.CompletedAt)),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}

// csvFormulaLeadChars are the characters that, at the start of a cell,
// common spreadsheet software (Excel, Google Sheets, LibreOffice Calc)
// reads as the opening of a formula rather than as literal text — this is
// true whether or not encoding/csv had to quote the cell for a comma or a
// newline, since a formula is recognised from the cell's content, not from
// how the file around it is escaped.
//
// customer_name and customer_phone are typed by whoever joined the line or
// booked online, with nothing here checking their shape beyond a length
// bound (see maxCustomerNameLen, maxPhoneLen), so a name of "=HYPERLINK(...)"
// or "=cmd|' /C calc'!A0" reaches this file exactly as typed. The row looks
// unremarkable in the download; the payload runs the moment an admin opens
// it in a spreadsheet to do payroll.
const csvFormulaLeadChars = "=+-@\t\r"

// csvFormulaSafe neutralizes a cell that would otherwise open as a formula,
// by prefixing it with a single quote — the same character a person types
// into Excel to say "treat this literally", and the standard mitigation
// for CSV injection (OWASP calls it out by name). Every spreadsheet that
// would have evaluated the cell instead treats the quote as its own
// force-text marker and renders the value beneath it, unchanged, to
// whoever opens the file.
//
// This does mean a phone number typed as "+44 7700 900000" carries a
// leading quote in the raw file too — the phone field is exactly as
// untrusted as the name field, and there is no way to tell "a customer's
// own number" from "an attempt to open a spreadsheet's formula parser"
// from the string alone. A spreadsheet hides the quote; a plain-text
// viewer of the CSV shows it, which is judged the better failure to have.
func csvFormulaSafe(s string) string {
	if s != "" && strings.ContainsRune(csvFormulaLeadChars, rune(s[0])) {
		return "'" + s
	}
	return s
}

// exportTime renders an optional instant, leaving the cell empty when the
// thing it records never happened.
func exportTime(t pgtype.Timestamptz, loc *time.Location) string {
	if !t.Valid {
		return ""
	}
	return t.Time.In(loc).Format(exportTimeLayout)
}

// exportSeconds renders a measured duration as a whole number of seconds,
// or an empty cell when there was nothing to measure.
func exportSeconds(d time.Duration, ok bool) string {
	if !ok {
		return ""
	}
	return strconv.Itoa(int(d.Seconds()))
}

// ExportFilename names the download after the queue and the day, so a
// folder of these sorts by date and nobody has to open one to find out
// which it is.
func ExportFilename(queueSlug string, day time.Time) string {
	return fmt.Sprintf("%s-%s.csv", queueSlug, day.Format(dateLayout))
}

package queue

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// namedTicket builds the fields the exporter reads.
func namedTicket(number int32, status Status, name, phone string) sqlc.QueueTicket {
	return sqlc.QueueTicket{
		Number:        number,
		Status:        string(status),
		CustomerName:  name,
		CustomerPhone: db.Text(phone),
	}
}

func parseCSV(t *testing.T, body string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	return rows
}

func exportToString(t *testing.T, tickets []sqlc.QueueTicket, loc *time.Location) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteCSV(&buf, tickets, loc); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	return buf.String()
}

func TestWriteCSVHeaderAndRowShape(t *testing.T) {
	ticket := namedTicket(1, StatusDone, "Ada Lovelace", "+44 7700 900000")
	ticket.CreatedAt = hhmm(9, 0)
	ticket.CalledAt = hhmm(9, 10)
	ticket.ServingAt = hhmm(9, 11)
	ticket.CompletedAt = hhmm(9, 21)

	rows := parseCSV(t, exportToString(t, []sqlc.QueueTicket{ticket}, reportLoc))

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want a header and one ticket", len(rows))
	}
	for i, want := range exportColumns {
		if rows[0][i] != want {
			t.Errorf("header column %d = %q, want %q", i, rows[0][i], want)
		}
	}
	if len(rows[1]) != len(exportColumns) {
		t.Fatalf("the ticket row has %d cells, want %d", len(rows[1]), len(exportColumns))
	}

	got := rows[1]
	if got[0] != "1" || got[1] != "done" || got[2] != "Ada Lovelace" || got[3] != "+44 7700 900000" {
		t.Errorf("row identity cells = %v, want number 1, done, Ada Lovelace, her phone", got[:4])
	}
	// 9:00 -> 9:10 is a 600-second wait; 9:11 -> 9:21 a 600-second service.
	if got[8] != "600" {
		t.Errorf("wait_seconds = %q, want %q", got[8], "600")
	}
	if got[9] != "600" {
		t.Errorf("service_seconds = %q, want %q", got[9], "600")
	}
}

// An empty cell is excluded from a spreadsheet average; a zero drags it
// down. A ticket cancelled before it was ever called has no wait, and the
// difference between those two is the difference between a truthful
// average and a wrong one.
func TestWriteCSVLeavesUnmeasurableCellsEmpty(t *testing.T) {
	ticket := namedTicket(2, StatusCancelled, "Grace Hopper", "")
	ticket.CreatedAt = hhmm(9, 0)
	ticket.CompletedAt = hhmm(9, 5)

	rows := parseCSV(t, exportToString(t, []sqlc.QueueTicket{ticket}, reportLoc))
	got := rows[1]

	for _, cell := range []struct {
		index int
		name  string
	}{{3, "customer_phone"}, {5, "called_at"}, {6, "serving_at"}, {8, "wait_seconds"}, {9, "service_seconds"}} {
		if got[cell.index] != "" {
			t.Errorf("%s = %q, want an empty cell", cell.name, got[cell.index])
		}
	}
	if got[4] == "" || got[7] == "" {
		t.Error("issued_at and completed_at should both be filled for this ticket")
	}
}

// The times are the org's own, because that is the day the row belongs
// to, and they carry their offset so a file opened elsewhere is not
// ambiguous about what the hour meant.
func TestWriteCSVWritesOrgLocalTimesWithOffset(t *testing.T) {
	ticket := namedTicket(1, StatusWaiting, "Ada", "")
	ticket.CreatedAt = hhmm(9, 30)

	rows := parseCSV(t, exportToString(t, []sqlc.QueueTicket{ticket}, reportLoc))
	issued := rows[1][4]

	if !strings.HasPrefix(issued, "2026-03-03 09:30:00") {
		t.Errorf("issued_at = %q, want the org-local 09:30", issued)
	}
	if !strings.HasSuffix(issued, "+05:30") {
		t.Errorf("issued_at = %q, want it to carry the org's UTC offset", issued)
	}
}

// A name with a comma, a quote or a newline in it must not be able to
// shift the columns of the rest of the file. encoding/csv handles this;
// the test is here so that a hand-rolled writer can never quietly replace
// it.
func TestWriteCSVQuotesAwkwardNames(t *testing.T) {
	awkward := []string{
		`Smith, John`,
		`He said "hello"`,
		"Two\nLines",
		`=1+1`,
	}

	tickets := make([]sqlc.QueueTicket, 0, len(awkward))
	for i, name := range awkward {
		ticket := namedTicket(int32(i+1), StatusWaiting, name, "")
		ticket.CreatedAt = hhmm(9, 0)
		tickets = append(tickets, ticket)
	}

	rows := parseCSV(t, exportToString(t, tickets, reportLoc))
	if len(rows) != len(awkward)+1 {
		t.Fatalf("got %d rows, want %d — a value broke the row structure", len(rows), len(awkward)+1)
	}
	for i, name := range awkward {
		if got := rows[i+1][2]; got != name {
			t.Errorf("row %d name = %q, want %q", i+1, got, name)
		}
		if len(rows[i+1]) != len(exportColumns) {
			t.Errorf("row %d has %d cells, want %d", i+1, len(rows[i+1]), len(exportColumns))
		}
	}
}

// A day with no tickets still produces a file with a header, so a
// spreadsheet opens it rather than reporting a broken download, and the
// answer reads as "nothing happened" instead of "the export failed".
func TestWriteCSVEmptyDayStillHasAHeader(t *testing.T) {
	rows := parseCSV(t, exportToString(t, nil, reportLoc))

	if len(rows) != 1 {
		t.Fatalf("got %d rows for an empty day, want just the header", len(rows))
	}
	if len(rows[0]) != len(exportColumns) {
		t.Errorf("header has %d columns, want %d", len(rows[0]), len(exportColumns))
	}
}

func TestWriteCSVPreservesTicketOrder(t *testing.T) {
	var tickets []sqlc.QueueTicket
	for n := int32(1); n <= 5; n++ {
		ticket := namedTicket(n, StatusDone, "Customer", "")
		ticket.CreatedAt = hhmm(9, int(n))
		tickets = append(tickets, ticket)
	}

	rows := parseCSV(t, exportToString(t, tickets, reportLoc))
	for i := 1; i < len(rows); i++ {
		if rows[i][0] != strconv.Itoa(i) {
			t.Fatalf("row %d has number %q, want the tickets in issue order", i, rows[i][0])
		}
	}
}

// A nil location must not panic — org.Location degrades to UTC on an
// unparseable zone, and the export should degrade the same way.
func TestWriteCSVToleratesNilLocation(t *testing.T) {
	ticket := namedTicket(1, StatusWaiting, "Ada", "")
	ticket.CreatedAt = hhmm(9, 0)

	if rows := parseCSV(t, exportToString(t, []sqlc.QueueTicket{ticket}, nil)); len(rows) != 2 {
		t.Fatalf("got %d rows, want a header and one ticket", len(rows))
	}
}

func TestExportFilename(t *testing.T) {
	got := ExportFilename("walk-ins", reportDay())
	if want := "walk-ins-2026-03-03.csv"; got != want {
		t.Errorf("ExportFilename = %q, want %q", got, want)
	}

	// The date is ISO so a folder of these sorts chronologically.
	earlier := ExportFilename("walk-ins", reportDay().AddDate(0, 0, -1))
	if earlier >= got {
		t.Errorf("%q does not sort before %q", earlier, got)
	}
}

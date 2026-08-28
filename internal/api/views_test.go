package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/queue"
)

var manila = mustLoad("Asia/Manila")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// boardWith builds a board the way the loader would, so the view tests
// exercise the same AheadOf and EstimateFor the real one does.
func boardWith(day time.Time, waiting []int32, inProgress []int32, st queue.ServiceTime) queue.Board {
	b := queue.Board{
		Queue:       sqlc.Queue{Slug: "walk-ins", Name: "Walk-ins", Active: true},
		Day:         day,
		ServiceTime: st,
		Stats:       queue.Stats{Waiting: len(waiting), Serving: len(inProgress)},
	}
	for _, n := range waiting {
		b.Waiting = append(b.Waiting, sqlc.QueueTicket{Number: n, Status: string(queue.StatusWaiting)})
	}
	for _, n := range inProgress {
		b.InProgress = append(b.InProgress, sqlc.QueueTicket{Number: n, Status: string(queue.StatusServing)})
	}
	return b
}

func TestNewOrgCarriesTheBusinessesOwnClock(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 30, 0, 0, manila)
	got := NewOrg(sqlc.Organization{Name: "Demo Clinic", Slug: "demo", Timezone: "Asia/Manila"}, now)

	if got.Name != "Demo Clinic" || got.Slug != "demo" || got.Timezone != "Asia/Manila" {
		t.Errorf("org = %+v", got)
	}
	if !got.LocalTime.Equal(now) {
		t.Errorf("local_time = %v, want %v", got.LocalTime, now)
	}
	// A viewer in another country asking "is this place open now" needs
	// the clinic's clock, not their own, so the offset has to survive.
	encoded, _ := json.Marshal(got)
	if !strings.Contains(string(encoded), "+08:00") {
		t.Errorf("local_time encoded without the org's offset: %s", encoded)
	}
}

// A price of zero means free. A price nobody has set means the org does
// not publish one here, and a front end has to be able to tell them apart
// to avoid printing "₱0.00" over every unpriced service.
func TestNewServiceDistinguishesFreeFromUnpriced(t *testing.T) {
	free := NewService(sqlc.Service{Slug: "consult", PriceCents: db.Int4(ptr(int32(0)))})
	if free.PriceCents == nil || *free.PriceCents != 0 {
		t.Errorf("price_cents = %v, want an explicit 0 for a free service", free.PriceCents)
	}

	unpriced := NewService(sqlc.Service{Slug: "consult"})
	if unpriced.PriceCents != nil {
		t.Errorf("price_cents = %d, want null when no price is set", *unpriced.PriceCents)
	}
}

// An org with nothing on offer has an empty list, not a missing one. A
// null here makes every caller guard a case the API never means.
func TestListsEncodeAsArraysWhenEmpty(t *testing.T) {
	services, err := json.Marshal(NewServices(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(services) != "[]" {
		t.Errorf("empty services encoded as %s, want []", services)
	}

	slots, err := json.Marshal(NewSlots(nil, manila))
	if err != nil {
		t.Fatal(err)
	}
	if string(slots) != "[]" {
		t.Errorf("empty slots encoded as %s, want []", slots)
	}
}

// The list a customer chooses between two lines from is the same numbers
// the board would show them once they had.
func TestNewQueueSummaryAgreesWithTheBoardsOwnEstimate(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{5, 6, 7, 8}, []int32{4}, queue.ServiceTime{Average: 12 * time.Minute, Samples: 30})

	got := NewQueueSummary(board)
	want := int(queue.Estimate(board.Stats.Waiting, board.ServiceTime, board.ActiveServers()).Minutes())

	if got.Waiting != 4 {
		t.Errorf("waiting = %d, want 4", got.Waiting)
	}
	if got.EstimatedWaitMinutes != want {
		t.Errorf("estimated_wait_minutes = %d, want %d", got.EstimatedWaitMinutes, want)
	}
	if got.Slug != "walk-ins" || !got.Open {
		t.Errorf("summary = %+v", got)
	}
}

func TestNewTicketCountsTheHolderIntoTheirOwnPosition(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{11, 12, 13}, []int32{10}, queue.ServiceTime{Average: 10 * time.Minute, Samples: 20})

	front := NewTicket(sqlc.QueueTicket{Number: 11, Status: string(queue.StatusWaiting)}, board, manila)
	if front.Position == nil || *front.Position != 1 {
		t.Errorf("position = %v, want 1 — the front of the line is first, not zeroth", front.Position)
	}

	third := NewTicket(sqlc.QueueTicket{Number: 13, Status: string(queue.StatusWaiting)}, board, manila)
	if third.Position == nil || *third.Position != 3 {
		t.Errorf("position = %v, want 3", third.Position)
	}
	if third.EstimatedWaitMinutes == nil {
		t.Fatal("a waiting ticket has no wait estimate")
	}
	want := int(board.EstimateFor(13).Minutes())
	if *third.EstimatedWaitMinutes != want {
		t.Errorf("estimated_wait_minutes = %d, want %d", *third.EstimatedWaitMinutes, want)
	}
}

// Somebody at the counter has no place in a line, and a resolved ticket
// has no wait left. Sending a number for either invites a front end to
// render a countdown for a customer who has already gone home.
func TestNewTicketDropsPositionOnceTheTicketLeavesTheLine(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{11, 12}, []int32{10}, queue.ServiceTime{})

	for _, status := range []queue.Status{
		queue.StatusCalled, queue.StatusServing,
		queue.StatusDone, queue.StatusNoShow, queue.StatusCancelled,
	} {
		got := NewTicket(sqlc.QueueTicket{Number: 10, Status: string(status)}, board, manila)
		if got.Position != nil {
			t.Errorf("%s: position = %d, want null", status, *got.Position)
		}
		if got.EstimatedWaitMinutes != nil {
			t.Errorf("%s: estimated_wait_minutes = %d, want null", status, *got.EstimatedWaitMinutes)
		}
	}
}

// Cancellable is on the wire so the rule lives in one language. Deriving
// it in the front end would mean the button and the endpoint could come to
// different conclusions, and the endpoint is the one with the tests.
func TestNewTicketReportsCancellabilityTheWayTheDomainDoes(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{11}, nil, queue.ServiceTime{})

	for _, status := range queue.AllStatuses() {
		got := NewTicket(sqlc.QueueTicket{Number: 11, Status: string(status)}, board, manila)
		if got.Cancellable != queue.CustomerCancellable(status) {
			t.Errorf("%s: cancellable = %v, want %v", status, got.Cancellable, queue.CustomerCancellable(status))
		}
		if got.StatusLabel != queue.Label(status) {
			t.Errorf("%s: status_label = %q, want %q", status, got.StatusLabel, queue.Label(status))
		}
	}
}

// The ticket's day is the board's day, so a link reopened tomorrow still
// says which day the number belonged to.
func TestNewTicketReportsTheBoardsServiceDay(t *testing.T) {
	day := time.Date(2026, 4, 1, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{3}, nil, queue.ServiceTime{})

	got := NewTicket(sqlc.QueueTicket{Number: 3, Status: string(queue.StatusWaiting)}, board, manila)
	if got.ServiceDay != "2026-04-01" {
		t.Errorf("service_day = %q, want the board's own day", got.ServiceDay)
	}
}

// The token names an instant and a staff member, and it has to survive a
// round trip through the front end unchanged — that is its entire job.
func TestSlotTokensRoundTripBackToWhatTheyName(t *testing.T) {
	start := time.Date(2026, 4, 2, 10, 0, 0, 0, manila)
	open := []appointment.OpenSlot{{
		Start:       start,
		End:         start.Add(30 * time.Minute),
		StaffUserID: "11111111-1111-1111-1111-111111111111",
		StaffName:   "Dana",
	}}

	got := NewSlots(open, manila)
	if len(got) != 1 {
		t.Fatalf("got %d slots, want 1", len(got))
	}
	if got[0].StaffName != "Dana" {
		t.Errorf("staff_name = %q, want Dana", got[0].StaffName)
	}

	parsedStart, staffID, ok := appointment.ParseSlotToken(got[0].Token, manila)
	if !ok {
		t.Fatalf("token %q did not parse", got[0].Token)
	}
	if !parsedStart.Equal(start) {
		t.Errorf("token start = %v, want %v", parsedStart, start)
	}
	if staffID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("token staff = %q", staffID)
	}
}

// Slot times go out in the org's zone. A clinic's 10:00 is the clinic's
// 10:00 wherever the customer is reading from.
func TestSlotsAreRenderedInTheOrgsZone(t *testing.T) {
	start := time.Date(2026, 4, 2, 2, 0, 0, 0, time.UTC) // 10:00 in Manila
	got := NewSlots([]appointment.OpenSlot{{Start: start, End: start.Add(time.Hour), StaffName: "Dana"}}, manila)

	if h := got[0].Start.In(manila).Hour(); h != 10 {
		t.Errorf("slot hour = %d, want 10 in the org's zone", h)
	}
	encoded, _ := json.Marshal(got[0])
	if !strings.Contains(string(encoded), "+08:00") {
		t.Errorf("slot encoded without the org's offset: %s", encoded)
	}
}

// The cancel button is offered on exactly the appointments CancelPublic
// will accept, so a customer is never shown an action that fails.
func TestNewAppointmentOffersCancelOnlyBeforeItStarts(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, manila)
	row := func(status string, startsAt time.Time) sqlc.GetAppointmentWithNamesRow {
		return sqlc.GetAppointmentWithNamesRow{
			CustomerName: "Ada",
			ServiceName:  "Consultation",
			StaffName:    "Dana",
			Status:       status,
			StartsAt:     db.Timestamptz(startsAt),
			EndsAt:       db.Timestamptz(startsAt.Add(30 * time.Minute)),
		}
	}

	upcoming := NewAppointment(row("booked", now.Add(2*time.Hour)), now, manila)
	if !upcoming.Cancellable {
		t.Error("an upcoming booked appointment should be cancellable")
	}

	started := NewAppointment(row("booked", now.Add(-10*time.Minute)), now, manila)
	if started.Cancellable {
		t.Error("an appointment that has already started must not offer a cancel button")
	}

	for _, status := range []string{"cancelled", "completed", "no_show"} {
		got := NewAppointment(row(status, now.Add(2*time.Hour)), now, manila)
		if got.Cancellable {
			t.Errorf("%s: cancellable = true, want false — the lifecycle is terminal", status)
		}
	}
}

// The board and the summary are served to any origin without a session,
// so what they leave out is part of the contract. This asserts the absence
// directly, the way the existing board API's test does.
func TestQueueSummaryCarriesNoPersonalData(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, manila)
	board := boardWith(day, []int32{2}, []int32{1}, queue.ServiceTime{})
	board.Waiting[0].CustomerName = "Grace Hopper"
	board.InProgress[0].CustomerName = "Ada Lovelace"
	board.InProgress[0].CustomerPhone = db.Text("+63 917 000 0000")

	encoded, err := json.Marshal(NewQueueSummary(board))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Grace Hopper", "Ada Lovelace", "917 000 0000"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the queue summary contains %q:\n%s", secret, encoded)
		}
	}
}

// A nil location is a programmer error the API should survive rather than
// panic on: an unparseable stored timezone already degrades to UTC
// everywhere else, and a nil dereference in a public handler is a 500 for
// a customer standing in a shop.
func TestViewsFallBackToUTCRatherThanPanicOnANilZone(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	board := boardWith(day, []int32{1}, nil, queue.ServiceTime{})

	NewTicket(sqlc.QueueTicket{Number: 1, Status: string(queue.StatusWaiting)}, board, nil)
	NewSlots([]appointment.OpenSlot{{Start: day, End: day.Add(time.Hour)}}, nil)
	NewAppointment(sqlc.GetAppointmentWithNamesRow{
		Status:   "booked",
		StartsAt: db.Timestamptz(day),
		EndsAt:   db.Timestamptz(day.Add(time.Hour)),
	}, day, nil)
}

func ptr[T any](v T) *T { return &v }

package queue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

func apiBoard(active bool, waiting []int32, inProgress []int32, stats Stats) Board {
	b := Board{
		Queue: sqlc.Queue{Name: "Walk-ins", Active: active},
		Day:   time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
		Stats: stats,
	}
	for _, n := range waiting {
		b.Waiting = append(b.Waiting, sqlc.QueueTicket{Number: n, Status: string(StatusWaiting)})
	}
	for _, n := range inProgress {
		b.InProgress = append(b.InProgress, sqlc.QueueTicket{Number: n, Status: string(StatusServing)})
	}
	return b
}

func TestPublicStatusReportsTheBoardItWasBuiltFrom(t *testing.T) {
	board := apiBoard(true, []int32{12, 13, 14}, []int32{11},
		Stats{Waiting: 3, Called: 1, Serving: 1, Done: 8, NoShow: 1})
	board.ServiceTime = ServiceTime{Average: 10 * time.Minute, Samples: 20}

	got := NewPublicStatus(sqlc.Organization{Name: "Demo Clinic"}, board, time.Now())

	if got.Organization != "Demo Clinic" || got.Queue != "Walk-ins" {
		t.Errorf("status names %q / %q", got.Organization, got.Queue)
	}
	if !got.Open {
		t.Error("an active queue reported as closed")
	}
	if got.NowServing == nil || *got.NowServing != 11 {
		t.Errorf("now_serving = %v, want the lowest number in progress (11)", got.NowServing)
	}
	if got.Waiting != 3 {
		t.Errorf("waiting = %d, want 3", got.Waiting)
	}
	if got.InProgress != 2 {
		t.Errorf("in_progress = %d, want 2 — called and serving both occupy a counter", got.InProgress)
	}
	if got.Served != 8 {
		t.Errorf("served_today = %d, want 8", got.Served)
	}
	if got.ServiceDay != "2026-04-02" {
		t.Errorf("service_day = %q, want the board's own day", got.ServiceDay)
	}
}

// The number a website prints and the number on the sign above the door
// have to be the same number, or a customer reads one and is told the
// other on arrival.
func TestPublicStatusWaitMatchesTheBoardsOwnEstimate(t *testing.T) {
	board := apiBoard(true, []int32{5, 6, 7, 8}, []int32{4}, Stats{Waiting: 4, Serving: 1})
	board.ServiceTime = ServiceTime{Average: 12 * time.Minute, Samples: 30}

	want := Estimate(board.Stats.Waiting, board.ServiceTime, board.ActiveServers())
	got := NewPublicStatus(sqlc.Organization{Name: "Demo"}, board, time.Now())

	if got.EstimatedWaitMinutes != int(want.Minutes()) {
		t.Errorf("estimated_wait_minutes = %d, want %d — the API and the board must agree",
			got.EstimatedWaitMinutes, int(want.Minutes()))
	}
}

// Nobody at the counter is not the same fact as "ticket zero", and a
// display that renders it as a zero is showing a number no customer holds.
func TestPublicStatusLeavesNowServingNullWhenNobodyIsBeingServed(t *testing.T) {
	board := apiBoard(true, []int32{1, 2}, nil, Stats{Waiting: 2})

	got := NewPublicStatus(sqlc.Organization{Name: "Demo"}, board, time.Now())
	if got.NowServing != nil {
		t.Fatalf("now_serving = %d, want null", *got.NowServing)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"now_serving":null`) {
		t.Errorf("encoded as %s, want an explicit null", encoded)
	}
}

// A closed line still answers. A display that went blank or froze on the
// last number it saw would tell a room that the queue is broken rather
// than shut.
func TestPublicStatusReportsAClosedQueueWithItsNumbers(t *testing.T) {
	board := apiBoard(false, nil, nil, Stats{Done: 14})

	got := NewPublicStatus(sqlc.Organization{Name: "Demo"}, board, time.Now())
	if got.Open {
		t.Error("a closed queue reported as open")
	}
	if got.Served != 14 {
		t.Errorf("served_today = %d, want 14 — a closed queue still had a day", got.Served)
	}
}

// The payload is served without a session and to any origin, so what it
// does not contain is part of its contract. This asserts the absence
// directly: a field added to the board and forwarded here without thought
// is exactly the change that would leak a customer's name to a shop's
// website.
func TestPublicStatusCarriesNoPersonalData(t *testing.T) {
	board := apiBoard(true, []int32{2}, []int32{1}, Stats{Waiting: 1, Serving: 1})
	board.InProgress[0].CustomerName = "Ada Lovelace"
	board.InProgress[0].CustomerPhone = db.Text("+44 7700 900000")
	board.Waiting[0].CustomerName = "Grace Hopper"

	encoded, err := json.Marshal(NewPublicStatus(sqlc.Organization{Name: "Demo"}, board, time.Now()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, secret := range []string{"Ada Lovelace", "Grace Hopper", "7700 900000"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the public status payload contains %q:\n%s", secret, encoded)
		}
	}
}

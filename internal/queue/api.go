package queue

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// PublicStatus is one queue's live state as a machine reads it.
//
// It exists because a board is not the only screen a business owns. The
// ones that ask for this are a lobby television running someone else's
// signage software, a shop's own website wanting to say "4 people waiting
// right now", and an uptime check that wants to know a queue is moving.
// All three were previously reduced to scraping the HTML board, which
// breaks the next time a class name changes.
//
// It deliberately carries no names and no phone numbers — nothing the
// waiting-room display would not already show a whole room. That is what
// makes it safe to serve without a session and to allow from any origin:
// a business embedding this in its own site should not have to weigh
// whether doing so leaks its customers.
type PublicStatus struct {
	Organization string `json:"organization"`
	Queue        string `json:"queue"`
	// Open reports whether the line is accepting walk-ins. A closed queue
	// still answers, with its numbers: a display should be able to say
	// "closed" rather than go blank or show a stale count.
	Open bool `json:"open"`
	// NowServing is the number at the counter, or null when nothing is in
	// progress. Null rather than zero, because zero is a number a ticket
	// could plausibly have been given and "nobody" is not a count.
	NowServing *int32 `json:"now_serving"`
	Waiting    int    `json:"waiting"`
	InProgress int    `json:"in_progress"`
	Served     int    `json:"served_today"`
	// EstimatedWaitMinutes is what somebody joining the line now can
	// expect, on the same model and the same rounding the public board
	// shows. Serving one number from two calculations is how a website
	// ends up contradicting the sign above the door.
	EstimatedWaitMinutes int `json:"estimated_wait_minutes"`
	// ServiceDay is the business's own day, not UTC's, so a caller can
	// tell whether the numbers rolled over.
	ServiceDay  string    `json:"service_day"`
	GeneratedAt time.Time `json:"generated_at"`
}

// NewPublicStatus renders a board as the API's view of it.
func NewPublicStatus(o sqlc.Organization, board Board, now time.Time) PublicStatus {
	status := PublicStatus{
		Organization:         o.Name,
		Queue:                board.Queue.Name,
		Open:                 board.Queue.Active,
		Waiting:              board.Stats.Waiting,
		InProgress:           board.Stats.Called + board.Stats.Serving,
		Served:               board.Stats.Done,
		EstimatedWaitMinutes: int(Estimate(board.Stats.Waiting, board.ServiceTime, board.ActiveServers()).Minutes()),
		ServiceDay:           board.Day.Format(dateLayout),
		GeneratedAt:          now,
	}
	if ns, ok := board.NowServing(); ok {
		number := ns.Number
		status.NowServing = &number
	}
	return status
}

// apiMaxAge is how long a caller may reuse a response.
//
// A queue moves in minutes, and the pages that poll this are on a fifteen-
// second timer already, so five seconds costs a viewer nothing they would
// notice while capping what a wall of screens in one building can ask of
// the database. It is public rather than private caching for the same
// reason the payload carries no names: there is nothing here that one
// viewer should not see through another's proxy.
const apiMaxAge = 5

// apiCacheControl is the header apiMaxAge is actually served as, built
// once from the constant rather than written out beside it. The two were
// previously separate literals, so raising the constant changed the
// comment explaining the cache window and nothing about the cache window.
var apiCacheControl = fmt.Sprintf("public, max-age=%d", apiMaxAge)

// GetPublicAPIStatus serves one queue's live state as JSON.
func (h *Handlers) GetPublicAPIStatus(w http.ResponseWriter, r *http.Request) {
	pc, board, ok := h.publicBoard(w, r)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", apiCacheControl)
	// Read-only, no personal data, no credentials accepted: a business
	// putting its own waiting time on its own website should not need a
	// proxy to do it. Nothing here is scoped to who is asking, so there is
	// no cross-origin state for a permissive policy to leak.
	w.Header().Set("Access-Control-Allow-Origin", "*")

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(NewPublicStatus(pc.orgRow, board, time.Now().In(pc.loc))); err != nil {
		// The body has already begun, so this cannot become a 500. The
		// caller gets truncated JSON either way; logging is what makes it
		// visible rather than silent.
		h.log.Error("GetPublicAPIStatus: encode", "error", err, "queue", db.UUIDString(board.Queue.ID))
	}
}

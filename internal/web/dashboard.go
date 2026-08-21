package web

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/queue"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// QueueLine is one walk-in queue as the dashboard shows it: the line
// itself and how its day is going.
type QueueLine struct {
	Queue sqlc.Queue
	Stats queue.Stats
}

// Dashboard is the answer to "what is happening in my business right
// now", assembled once for the page that opens when somebody signs in.
//
// It exists because /app was a welcome message and a list of links. The
// first thing anyone opening a queue app wants is the state of the floor:
// how many people are waiting, who is next through the door, and whether
// anything needs attention — none of which was visible without picking a
// queue and opening its console.
//
// Every field is a value, and every derived number below is a method on
// them, so the arithmetic a business reads as "we have eleven people
// waiting" is testable without a database.
type Dashboard struct {
	Role         tenant.Role
	Name         string
	OrgName      string
	OrgSlug      string
	Day          time.Time
	Now          time.Time
	Lines        []QueueLine
	Appointments []sqlc.ListAppointmentsForOrgOnDayRow
	// Bookable is how many services a customer could book online right
	// now. Zero is worth saying out loud: the public booking pages exist
	// and are reachable, but they have nothing to offer until somebody is
	// assigned to a service.
	Bookable  int
	CSRFToken string
}

// Waiting is everyone standing in every line, which is the number a
// manager glancing at a screen actually wants.
func (d Dashboard) Waiting() int {
	total := 0
	for _, l := range d.Lines {
		total += l.Stats.Waiting
	}
	return total
}

// InProgress is how many customers are at a counter across all queues,
// called or being served.
func (d Dashboard) InProgress() int {
	total := 0
	for _, l := range d.Lines {
		total += l.Stats.Called + l.Stats.Serving
	}
	return total
}

// ServedToday counts the tickets that made it through, across every line.
func (d Dashboard) ServedToday() int {
	total := 0
	for _, l := range d.Lines {
		total += l.Stats.Done
	}
	return total
}

// LongestLine is the queue with the most people waiting — where a spare
// member of staff should go. It reports ok=false when nobody is waiting
// anywhere, so the page says the floor is clear rather than naming a
// queue with nothing in it.
//
// A tie takes the first by name, matching the order the lines are listed
// in: pointing at a different queue than the one highlighted below would
// read as a disagreement rather than as a tie.
func (d Dashboard) LongestLine() (QueueLine, bool) {
	var best QueueLine
	found := false
	for _, l := range d.Lines {
		if l.Stats.Waiting > best.Stats.Waiting {
			best, found = l, true
		}
	}
	return best, found
}

// Booked is how many of today's appointments are still expected. A
// cancelled or completed one is not something anybody has to prepare for.
func (d Dashboard) Booked() int {
	count := 0
	for _, a := range d.Appointments {
		if appointment.Status(a.Status) == appointment.StatusBooked {
			count++
		}
	}
	return count
}

// NextAppointment is the next booking still to come today: who is due,
// when, and with whom.
//
// "Still to come" is measured from Now rather than from the start of the
// day, because the question the page is answering is "who is walking
// through the door next", and an appointment that started twenty minutes
// ago is not the answer. It returns ok=false once the day's bookings are
// behind us, which the page renders as a sentence.
func (d Dashboard) NextAppointment() (sqlc.ListAppointmentsForOrgOnDayRow, bool) {
	var next sqlc.ListAppointmentsForOrgOnDayRow
	found := false
	for _, a := range d.Appointments {
		if appointment.Status(a.Status) != appointment.StatusBooked {
			continue
		}
		start := db.Time(a.StartsAt)
		if !start.After(d.Now) {
			continue
		}
		if !found || start.Before(db.Time(next.StartsAt)) {
			next, found = a, true
		}
	}
	return next, found
}

// Quiet reports whether there is nothing going on at all: no one waiting,
// no one at a counter, and nothing booked for the rest of the day. The
// page says so plainly instead of showing a wall of zeroes.
func (d Dashboard) Quiet() bool {
	if d.Waiting() > 0 || d.InProgress() > 0 {
		return false
	}
	_, upcoming := d.NextAppointment()
	return !upcoming
}

// handleAppHome renders the dashboard.
//
// It is open to staff as well as admins, and shows both of them the same
// floor. What differs is the links: configuration is admin-only
// everywhere else in the app, so offering staff a shortcut to a page that
// would answer them with a 403 is worse than not offering it.
func (s *Server) handleAppHome(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.CurrentIdentity(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	board, err := s.loadDashboard(r.Context(), ident.OrgID)
	if err != nil {
		s.log.Error("handleAppHome: load dashboard", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	board.Role = ident.Role
	board.Name = ident.Name
	board.CSRFToken = ident.CSRFToken

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := AppHome(board).Render(r.Context(), w); err != nil {
		s.log.Error("handleAppHome: render", "error", err)
	}
}

// loadDashboard reads today's state for one org.
//
// The per-queue stats are read one query per queue rather than in a single
// grouped statement. An org has a handful of queues, not thousands, and
// the alternative is a second aggregate that has to be kept in step with
// the one the board already uses — two ways of counting the same tickets
// is how a dashboard ends up disagreeing with the console it links to.
func (s *Server) loadDashboard(ctx context.Context, orgID string) (Dashboard, error) {
	orgRow, err := sqlc.New(s.pool).GetOrganizationByID(ctx, db.MustUUID(orgID))
	if err != nil {
		return Dashboard{}, err
	}
	loc := org.LocationOf(orgRow)
	now := time.Now().In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	queues, err := queue.ListActive(ctx, s.pool, orgID)
	if err != nil {
		return Dashboard{}, err
	}

	lines := make([]QueueLine, 0, len(queues))
	for _, q := range queues {
		board, err := queue.LoadBoard(ctx, s.pool, q, day)
		if err != nil {
			return Dashboard{}, err
		}
		lines = append(lines, QueueLine{Queue: q, Stats: board.Stats})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Queue.Name < lines[j].Queue.Name })

	appts, err := appointment.ListForOrgOnDay(ctx, s.pool, orgID, day)
	if err != nil {
		return Dashboard{}, err
	}

	bookable, err := appointment.ListBookableServices(ctx, s.pool, orgID)
	if err != nil {
		return Dashboard{}, err
	}

	return Dashboard{
		OrgName:      orgRow.Name,
		OrgSlug:      orgRow.Slug,
		Day:          day,
		Now:          now,
		Lines:        lines,
		Appointments: appts,
		Bookable:     len(bookable),
	}, nil
}

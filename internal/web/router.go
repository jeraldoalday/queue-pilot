package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/api"
	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/config"
	sqlcgen "github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/metrics"
	"github.com/jeraldoalday/queuepilot/internal/platform/ratelimit"
	"github.com/jeraldoalday/queuepilot/internal/queue"
	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type Server struct {
	cfg          config.Config
	log          *slog.Logger
	pool         *pgxpool.Pool
	queries      *sqlcgen.Queries
	sessions     *session.Store
	auth         *auth.Handlers
	catalog      *catalog.Handlers
	availability *availability.Handlers
	appointment  *appointment.Handlers
	org          *org.Handlers
	queue        *queue.Handlers
	api          *api.Handlers
	publicLimit  *ratelimit.Limiter
	authLimit    *ratelimit.Limiter
	// metrics is nil when METRICS_TOKEN is unset, and the middleware then
	// leaves every request untouched rather than timing work nobody can
	// read.
	metrics   *metrics.Registry
	startedAt time.Time
}

func NewServer(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) *Server {
	sessions := session.NewStore(pool, clock.Real{})
	return &Server{
		cfg:          cfg,
		log:          log,
		pool:         pool,
		queries:      sqlcgen.New(pool),
		sessions:     sessions,
		auth:         auth.NewHandlers(pool, sessions, cfg.SessionCookieSecure, log),
		catalog:      catalog.NewHandlers(pool, log),
		availability: availability.NewHandlers(pool, log),
		appointment:  appointment.NewHandlers(pool, log),
		org:          org.NewHandlers(pool, log),
		queue:        queue.NewHandlers(pool, log),
		api:          api.NewHandlers(pool, log),
		publicLimit:  ratelimit.New(publicBurst, publicInterval, clock.Real{}),
		authLimit:    ratelimit.New(authBurst, authInterval, clock.Real{}),
		metrics:      newRegistry(cfg),
		startedAt:    time.Now(),
	}
}

// newRegistry returns a registry only when the endpoint that would expose
// it is configured. Counting into a registry nothing can read is memory
// spent on nothing.
func newRegistry(cfg config.Config) *metrics.Registry {
	if cfg.MetricsToken == "" {
		return nil
	}
	return metrics.New()
}

// publicBurst and publicInterval bound how fast one client can take
// tickets from the unauthenticated join form. That endpoint has no session
// and no CSRF token to check — a public kiosk cannot have either — so this
// is what stops one person or script from filling a business's line with
// junk numbers. The allowance is generous on purpose: a family arriving
// together shares an IP and must not be blocked, while a script taking
// hundreds is stopped well short of ruining anyone's morning.
const (
	publicBurst    = 5
	publicInterval = 30 * time.Second
)

// authBurst and authInterval bound how fast one client can attempt to
// sign in. The public forms above protect a business's queue from being
// filled with junk; this protects an account from being guessed into.
// Login already equalises its own timing between a wrong password and an
// unknown address (see EqualiseVerifyCost) so that one answer cannot be
// told from the other by how long it took — but that care buys nothing
// against a client simply given unlimited tries, since no timing
// side-channel is needed to find the right password eventually if wrong
// ones cost nothing. The allowance is tighter than the public forms':
// ten attempts is room for a person who mistypes a password a few times,
// not for a list of common ones.
const (
	authBurst    = 10
	authInterval = time.Minute
)

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", staticHandler())

	mux.HandleFunc("GET /robots.txt", s.handleRobots)
	// Off unless METRICS_TOKEN is set, and a 404 rather than a 401 when
	// it is not: whether this deployment collects metrics is not
	// something a stranger needs told.
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /{$}", s.handleHome)

	mux.HandleFunc("GET /signup", s.auth.GetSignup)
	mux.HandleFunc("POST /signup", s.auth.PostSignup)
	mux.HandleFunc("GET /login", s.auth.GetLogin)
	// Limited for the reason authBurst is: a sign-in form with no limit is
	// an unlimited number of guesses at somebody's password.
	mux.Handle("POST /login", ratelimit.Middleware(s.authLimit, s.cfg.TrustProxyHeaders, s.log)(http.HandlerFunc(s.auth.PostLogin)))

	requireAuth := auth.RequireAuth(s.sessions, s.log)
	requireAdmin := auth.RequireRole(tenant.RoleAdmin)

	mux.Handle("POST /logout", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.auth.PostLogout))))
	mux.Handle("GET /app", requireAuth(http.HandlerFunc(s.handleAppHome)))
	// Settings are org configuration, so admin-only — and the timezone in
	// particular decides when every queue's day rolls over.
	mux.Handle("GET /app/settings", requireAuth(requireAdmin(http.HandlerFunc(s.org.GetSettings))))
	mux.Handle("POST /app/settings", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.org.PostSettings)))))

	mux.Handle("GET /app/staff", requireAuth(requireAdmin(http.HandlerFunc(s.auth.GetStaffList))))
	mux.Handle("GET /app/staff/new", requireAuth(requireAdmin(http.HandlerFunc(s.auth.GetNewStaff))))
	mux.Handle("POST /app/staff", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.auth.PostNewStaff)))))
	// Suspending an account decides who can sign in, so it belongs with the
	// rest of org configuration rather than with the day's work.
	mux.Handle("POST /app/staff/{staffID}/access", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.auth.PostStaffDisabled)))))

	mux.Handle("GET /app/staff/{staffID}/availability", requireAuth(requireAdmin(http.HandlerFunc(s.availability.GetAvailability))))
	mux.Handle("POST /app/staff/{staffID}/availability", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.availability.PostAvailability)))))
	mux.Handle("DELETE /app/staff/{staffID}/availability/{id}", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.availability.DeleteAvailabilityHandler)))))

	mux.Handle("GET /app/services", requireAuth(requireAdmin(http.HandlerFunc(s.catalog.GetServices))))
	mux.Handle("POST /app/services", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.catalog.PostServices)))))
	mux.Handle("GET /app/services/{id}", requireAuth(requireAdmin(http.HandlerFunc(s.catalog.GetServiceRow))))
	mux.Handle("GET /app/services/{id}/edit", requireAuth(requireAdmin(http.HandlerFunc(s.catalog.GetServiceEdit))))
	mux.Handle("PUT /app/services/{id}", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.catalog.PutService)))))
	mux.Handle("DELETE /app/services/{id}", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.catalog.DeleteServiceHandler)))))
	mux.Handle("POST /app/services/{id}/staff/{staffID}/toggle", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.catalog.PostToggleStaff)))))

	// Scheduling is available to staff as well as admins — staff need to
	// see and book the day's appointments, unlike org configuration.
	mux.Handle("GET /app/schedule", requireAuth(http.HandlerFunc(s.appointment.GetSchedule)))
	mux.Handle("POST /app/appointments/slots", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.appointment.PostSlots))))
	mux.Handle("POST /app/appointments", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.appointment.PostAppointment))))
	mux.Handle("POST /app/appointments/{id}/status", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.appointment.PostStatus))))

	// Creating and closing queues is org configuration, so it is
	// admin-only; working a queue is the job, so the console and every
	// ticket action are open to staff too.
	mux.Handle("GET /app/queues", requireAuth(requireAdmin(http.HandlerFunc(s.queue.GetQueues))))
	mux.Handle("POST /app/queues", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.queue.PostQueues)))))
	mux.Handle("POST /app/queues/{id}", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.queue.PostQueue)))))
	mux.Handle("DELETE /app/queues/{id}", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.queue.DeleteQueue)))))

	mux.Handle("GET /app/queues/{id}/console", requireAuth(http.HandlerFunc(s.queue.GetConsole)))
	// The report is staff-visible for the same reason the console is: the
	// people working the counter are the ones who act on when the rush was.
	mux.Handle("GET /app/queues/{id}/report", requireAuth(http.HandlerFunc(s.queue.GetReport)))
	// The export is admin-only: its rows carry customer names and phone
	// numbers, and a downloaded file outlives the reason it was pulled.
	mux.Handle("GET /app/queues/{id}/export.csv", requireAuth(requireAdmin(http.HandlerFunc(s.queue.GetExport))))
	mux.Handle("GET /app/queues/{id}/board", requireAuth(http.HandlerFunc(s.queue.GetConsoleBoard)))
	mux.Handle("POST /app/queues/{id}/call-next", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostCallNext))))
	mux.Handle("POST /app/queues/{id}/tickets", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostTicket))))
	mux.Handle("POST /app/tickets/{id}/status", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostTicketStatus))))

	// Public booking pages. Same rule as the public queue pages below:
	// no session, the tenant comes from the {orgSlug} segment, and every
	// lookup under it is scoped by the org ID that resolves to.
	mux.HandleFunc("GET /book/{orgSlug}", s.appointment.GetPublicServices)
	mux.HandleFunc("GET /book/{orgSlug}/{serviceSlug}", s.appointment.GetPublicBook)
	mux.HandleFunc("GET /book/{orgSlug}/a/{appointmentID}", s.appointment.GetPublicAppointmentPage)
	// The same booking as a file a calendar can read. It is a GET with no
	// session, exactly like the page beside it, and it is what makes the
	// booking survive the tab being closed.
	mux.HandleFunc("GET /book/{orgSlug}/a/{appointmentID}/calendar.ics", s.appointment.GetPublicAppointmentICS)

	// Public queue pages. These carry no session: the tenant comes from
	// the {orgSlug} path segment, and every lookup below it is scoped by
	// the org ID that resolves to — the same rule the authenticated side
	// follows, with a different source for the tenant.
	// A business's front door for walk-ins: every open line, shortest
	// wait first. The board below is the page for somebody who has
	// already chosen a line; this is the page they choose from.
	mux.HandleFunc("GET /q/{orgSlug}", s.queue.GetPublicLines)
	mux.HandleFunc("GET /q/{orgSlug}/lines", s.queue.GetPublicLinesPartial)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}", s.queue.GetPublicBoard)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/board", s.queue.GetPublicBoardPartial)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/display", s.queue.GetPublicDisplay)
	// The same board as JSON, for a screen this app does not render: a
	// lobby television running someone else's signage, or a business
	// putting its own waiting time on its own website. It carries no
	// names, so it is served under the same rules as the display.
	mux.HandleFunc("GET /api/q/{orgSlug}/{queueSlug}", s.queue.GetPublicAPIStatus)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/display/board", s.queue.GetPublicDisplayPartial)
	// Only the write is limited. The board and ticket pages poll on a
	// timer, so limiting reads would throttle the feature's own refresh;
	// taking a ticket is the action that costs a queue something.
	limitPublic := ratelimit.Middleware(s.publicLimit, s.cfg.TrustProxyHeaders, s.log)
	mux.Handle("POST /q/{orgSlug}/{queueSlug}/tickets", limitPublic(http.HandlerFunc(s.queue.PostPublicTicket)))
	// The two public booking writes share that limit for the same reason:
	// a form nobody has to sign in to reach is one a script can submit in
	// a loop, and a schedule filled with junk appointments costs a
	// business its whole day rather than one number on a board.
	mux.Handle("POST /book/{orgSlug}/{serviceSlug}", limitPublic(http.HandlerFunc(s.appointment.PostPublicBook)))
	mux.Handle("POST /book/{orgSlug}/a/{appointmentID}/cancel", limitPublic(http.HandlerFunc(s.appointment.PostPublicCancel)))
	// The JSON API, under a version so the payloads can change without
	// breaking a front end pinned to the old ones. Every route here is
	// unauthenticated and tenant-scoped by {orgSlug}, exactly like the
	// public HTML above it — see internal/api for why that is safe.
	//
	// The two writes share the public forms' rate limit. They are the same
	// two actions, reached by a different content type, and a limit one
	// spelling of a request obeys while the other does not is not a limit.
	mux.Handle("/api/v1/", CORS(s.apiRoutes(s.limitAPI())))

	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/t/{ticketID}", s.queue.GetPublicTicket)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/t/{ticketID}/status", s.queue.GetPublicTicketPartial)
	// Leaving the line shares the join form's limit: both are writes a
	// public page offers to someone with no session, and a script able to
	// do either quickly is a script able to disrupt a business's morning.
	mux.Handle("POST /q/{orgSlug}/{queueSlug}/t/{ticketID}/cancel", limitPublic(http.HandlerFunc(s.queue.PostPublicTicketCancel)))

	// The order of these four is deliberate, read outermost first.
	//
	// RequestID is first so every line any of the others writes carries
	// the same identifier — including the access log's, and including a
	// panic's. AccessLog sits inside it so it can read that ID, and
	// outside RecoverPanic so a panicking request still produces a line
	// saying it returned 500 rather than vanishing from the log entirely.
	// RecoverPanic sits outside the mux, so a panic anywhere in a handler
	// becomes a logged 500 rather than a dropped connection.
	// Metrics sits inside RecoverPanic so a panicked request is counted
	// as the 500 it became rather than not at all — an endpoint failing
	// every time is exactly what a dashboard is for.
	// SecurityHeaders is innermost so its headers are set on
	// the response a handler is about to write, and so a 500 produced by
	// RecoverPanic still carries them — an error page is a page.
	return RequestID(AccessLog(s.log)(RecoverPanic(s.log)(Metrics(s.metrics)(SecurityHeaders(s.cfg.SessionCookieSecure)(mux)))))
}

// limitAPI is the same limit the public forms carry, refused in the
// API's own language.
//
// It shares the one Limiter rather than holding its own: the two writes
// under /api/v1 are the same two actions the forms offer, and a script
// that alternates between the two spellings of a request would otherwise
// get twice the allowance the limit claims to grant.
//
// What differs is only the refusal. Every failure this API produces is an
// error envelope with a code from a closed set, and a 429 that arrives as
// plain text is the one exception — which leaves a front end guessing
// from a status code under exactly the conditions the rest of the API was
// designed to be legible in.
func (s *Server) limitAPI() func(http.Handler) http.Handler {
	return ratelimit.MiddlewareWith(s.publicLimit, s.cfg.TrustProxyHeaders, s.log, s.api.RateLimited)
}

// apiRoutes is the JSON API's own mux, mounted whole so every route under
// /api/v1/ shares one CORS wrapper rather than each remembering to carry
// one. A path this mux does not know answers with the API's JSON 404
// rather than the HTML router's page — a front end parsing an error should
// not have to guess whether it got JSON.
func (s *Server) apiRoutes(limitPublic func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}", s.api.GetOrg)

	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}/services/{serviceSlug}/slots", s.api.GetSlots)
	mux.Handle("POST /api/v1/orgs/{orgSlug}/services/{serviceSlug}/bookings", limitPublic(http.HandlerFunc(s.api.PostBooking)))
	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}/appointments/{appointmentID}", s.api.GetAppointment)
	// The same booking as a calendar file. It is under /api/v1 because a
	// front end is what offers the button, and it answers text/calendar
	// rather than JSON because the response is meant to be handed straight
	// to the operating system.
	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}/appointments/{appointmentID}/calendar.ics", s.api.GetAppointmentCalendar)
	mux.Handle("POST /api/v1/orgs/{orgSlug}/appointments/{appointmentID}/cancel", limitPublic(http.HandlerFunc(s.api.PostAppointmentCancel)))

	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}/queues/{queueSlug}", s.api.GetBoard)
	mux.Handle("POST /api/v1/orgs/{orgSlug}/queues/{queueSlug}/tickets", limitPublic(http.HandlerFunc(s.api.PostTicket)))
	mux.HandleFunc("GET /api/v1/orgs/{orgSlug}/queues/{queueSlug}/tickets/{ticketID}", s.api.GetTicket)
	mux.Handle("POST /api/v1/orgs/{orgSlug}/queues/{queueSlug}/tickets/{ticketID}/cancel", limitPublic(http.HandlerFunc(s.api.PostTicketCancel)))

	mux.HandleFunc("/api/v1/", s.api.NotFound)

	return mux
}

// handleHealthz reports readiness, including DB connectivity, so an
// orchestrator (Docker healthcheck, Fly.io, etc.) doesn't route traffic to
// an instance that can't reach Postgres.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.pool.Ping(r.Context()); err != nil {
		s.log.Error("healthz: db ping failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("db unavailable"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	count, err := s.queries.CountOrganizations(r.Context())
	if err != nil {
		s.log.Error("handleHome: count organizations", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Home(count).Render(r.Context(), w); err != nil {
		s.log.Error("handleHome: render", "error", err)
	}
}

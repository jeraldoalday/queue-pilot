package web

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/config"
	sqlcgen "github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
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
	queue        *queue.Handlers
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
		queue:        queue.NewHandlers(pool, log),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	fileServer := http.FileServer(http.Dir("internal/web/static"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /{$}", s.handleHome)

	mux.HandleFunc("GET /signup", s.auth.GetSignup)
	mux.HandleFunc("POST /signup", s.auth.PostSignup)
	mux.HandleFunc("GET /login", s.auth.GetLogin)
	mux.HandleFunc("POST /login", s.auth.PostLogin)

	requireAuth := auth.RequireAuth(s.sessions, s.log)
	requireAdmin := auth.RequireRole(tenant.RoleAdmin)

	mux.Handle("POST /logout", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.auth.PostLogout))))
	mux.Handle("GET /app", requireAuth(http.HandlerFunc(s.handleAppHome)))
	mux.Handle("GET /app/staff", requireAuth(requireAdmin(http.HandlerFunc(s.auth.GetStaffList))))
	mux.Handle("GET /app/staff/new", requireAuth(requireAdmin(http.HandlerFunc(s.auth.GetNewStaff))))
	mux.Handle("POST /app/staff", requireAuth(requireAdmin(auth.RequireCSRF(http.HandlerFunc(s.auth.PostNewStaff)))))

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
	mux.Handle("GET /app/queues/{id}/board", requireAuth(http.HandlerFunc(s.queue.GetConsoleBoard)))
	mux.Handle("POST /app/queues/{id}/call-next", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostCallNext))))
	mux.Handle("POST /app/queues/{id}/tickets", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostTicket))))
	mux.Handle("POST /app/tickets/{id}/status", requireAuth(auth.RequireCSRF(http.HandlerFunc(s.queue.PostTicketStatus))))

	// Public queue pages. These carry no session: the tenant comes from
	// the {orgSlug} path segment, and every lookup below it is scoped by
	// the org ID that resolves to — the same rule the authenticated side
	// follows, with a different source for the tenant.
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}", s.queue.GetPublicBoard)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/board", s.queue.GetPublicBoardPartial)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/display", s.queue.GetPublicDisplay)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/display/board", s.queue.GetPublicDisplayPartial)
	mux.HandleFunc("POST /q/{orgSlug}/{queueSlug}/tickets", s.queue.PostPublicTicket)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/t/{ticketID}", s.queue.GetPublicTicket)
	mux.HandleFunc("GET /q/{orgSlug}/{queueSlug}/t/{ticketID}/status", s.queue.GetPublicTicketPartial)

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

func (s *Server) handleAppHome(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.CurrentIdentity(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := AppHome(ident).Render(r.Context(), w); err != nil {
		s.log.Error("handleAppHome: render", "error", err)
	}
}

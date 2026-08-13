package web

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/config"
	sqlcgen "github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	queries *sqlcgen.Queries
}

func NewServer(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) *Server {
	return &Server{cfg: cfg, log: log, pool: pool, queries: sqlcgen.New(pool)}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	fileServer := http.FileServer(http.Dir("internal/web/static"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /{$}", s.handleHome)

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

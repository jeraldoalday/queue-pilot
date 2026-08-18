package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/config"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/queue"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// seedPassword is the login for every seeded account. It is fixed so the
// demo data is actually usable, which is also exactly why seeding refuses
// to run against a production database.
const seedPassword = "password123"

// seed populates the database with one worked-through organization: an
// admin, two staff, services with weekly hours, a walk-in queue and a line
// of customers partway through being served.
//
// It exists because reviewing this app means looking at a schedule with
// appointments on it and a queue with people in it. Clicking that state
// together by hand takes several minutes every time the database is reset.
func seed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	email := fs.String("email", "", "admin email for the seeded org (default: a unique demo address)")
	orgName := fs.String("org", "Demo Clinic", "name of the seeded organization")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Seeding writes fabricated customers and known-password accounts.
	// Refusing outright in production is the only safe default: the cost
	// of a wrong guess here is real data mixed with fake, or a working
	// login with a published password.
	if cfg.Env == "production" {
		return fmt.Errorf("seed: refusing to run with ENV=production")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	adminEmail := *email
	if adminEmail == "" {
		// Unique by default so repeated seeding doesn't collide on the
		// users_email_lower_idx unique index.
		adminEmail = "admin+" + id.New()[:8] + "@demo.test"
	}

	sessions := session.NewStore(pool, clock.Real{})
	ident, err := auth.Signup(ctx, pool, sessions, auth.SignupInput{
		OrgName:    *orgName,
		AdminName:  "Demo Admin",
		AdminEmail: adminEmail,
		Password:   seedPassword,
	})
	if err != nil {
		return fmt.Errorf("seed: create org: %w", err)
	}
	orgID := ident.OrgID

	staffIDs, err := seedStaff(ctx, pool, orgID)
	if err != nil {
		return err
	}
	if err := seedServices(ctx, pool, orgID, staffIDs); err != nil {
		return err
	}
	queueSlug, err := seedQueue(ctx, pool, orgID)
	if err != nil {
		return err
	}

	orgRow, err := sqlc.New(pool).GetOrganizationByID(ctx, db.MustUUID(orgID))
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, `seeded "%s"

  sign in    http://localhost:%s/login
  email      %s
  password   %s

  queue console  http://localhost:%s/app/queues
  public board   http://localhost:%s/q/%s/%s
  display screen http://localhost:%s/q/%s/%s/display
`,
		orgRow.Name,
		cfg.Port, adminEmail, seedPassword,
		cfg.Port,
		cfg.Port, orgRow.Slug, queueSlug,
		cfg.Port, orgRow.Slug, queueSlug,
	)
	return nil
}

func seedStaff(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]string, error) {
	names := []struct{ name, email string }{
		{"Rosa Diaz", "rosa+" + id.New()[:8] + "@demo.test"},
		{"Terry Jeffords", "terry+" + id.New()[:8] + "@demo.test"},
	}

	ids := make([]string, 0, len(names))
	for _, n := range names {
		user, err := auth.CreateStaffUser(ctx, pool, orgID, auth.CreateStaffInput{
			Name:     n.name,
			Email:    n.email,
			Password: seedPassword,
		})
		if err != nil {
			return nil, fmt.Errorf("seed: create staff %s: %w", n.name, err)
		}
		ids = append(ids, db.UUIDString(user.ID))
	}
	return ids, nil
}

func seedServices(ctx context.Context, pool *pgxpool.Pool, orgID string, staffIDs []string) error {
	services := []struct {
		name     string
		duration int
	}{
		{"Consultation", 30},
		{"Follow-up", 15},
	}

	for _, s := range services {
		svc, err := catalog.CreateService(ctx, pool, orgID, catalog.ServiceInput{
			Name:            s.name,
			DurationMinutes: s.duration,
		})
		if err != nil {
			return fmt.Errorf("seed: create service %s: %w", s.name, err)
		}
		for _, staffID := range staffIDs {
			if err := catalog.ToggleStaffAssignment(ctx, pool, orgID, db.UUIDString(svc.ID), staffID); err != nil {
				return fmt.Errorf("seed: assign staff to %s: %w", s.name, err)
			}
		}
	}

	// Monday to Friday, 09:00-17:00, for everyone. Without hours there are
	// no bookable slots, and the schedule page would look broken rather
	// than empty.
	for _, staffID := range staffIDs {
		for weekday := int(time.Monday); weekday <= int(time.Friday); weekday++ {
			if _, err := availability.AddHours(ctx, pool, orgID, staffID, availability.HoursInput{
				Weekday:  weekday,
				StartMin: 9 * 60,
				EndMin:   17 * 60,
			}); err != nil {
				return fmt.Errorf("seed: add availability: %w", err)
			}
		}
	}
	return nil
}

// seedQueue creates a walk-in line and leaves it mid-service: one customer
// at the counter, one just called, and three still waiting. A queue seeded
// as an empty list would exercise none of the board.
func seedQueue(ctx context.Context, pool *pgxpool.Pool, orgID string) (string, error) {
	q, err := queue.Create(ctx, pool, orgID, queue.Input{Name: "Walk-ins"})
	if err != nil {
		return "", fmt.Errorf("seed: create queue: %w", err)
	}
	queueID := db.UUIDString(q.ID)

	loc, err := org.Location(ctx, pool, orgID)
	if err != nil {
		return "", err
	}
	now := time.Now().In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	customers := []string{"Ada Lovelace", "Grace Hopper", "Alan Turing", "Katherine Johnson", "Edsger Dijkstra"}
	tickets := make([]string, 0, len(customers))
	for _, name := range customers {
		t, err := queue.Issue(ctx, pool, orgID, queueID, day, queue.TicketInput{CustomerName: name})
		if err != nil {
			return "", fmt.Errorf("seed: issue ticket for %s: %w", name, err)
		}
		tickets = append(tickets, db.UUIDString(t.ID))
	}

	// #1 is at the counter, #2 has just been called, the rest wait.
	if _, err := queue.SetStatus(ctx, pool, orgID, tickets[0], queue.StatusCalled, ""); err != nil {
		return "", fmt.Errorf("seed: call first ticket: %w", err)
	}
	if _, err := queue.SetStatus(ctx, pool, orgID, tickets[0], queue.StatusServing, ""); err != nil {
		return "", fmt.Errorf("seed: serve first ticket: %w", err)
	}
	if _, err := queue.SetStatus(ctx, pool, orgID, tickets[1], queue.StatusCalled, ""); err != nil {
		return "", fmt.Errorf("seed: call second ticket: %w", err)
	}

	return q.Slug, nil
}

# QueuePilot

Appointment booking and walk-in queue management, built as a multi-tenant
Go service. Server-rendered with [templ](https://templ.guide) + htmx +
Tailwind CSS — one binary, no Node/npm build step.

## What it does

**Appointments.** Services with durations, staff assigned to the services
they perform, recurring weekly availability per staff member, and booking
against generated slots. Double-booking is prevented by a Postgres
`EXCLUDE` constraint, not only by an application check, so it holds under
concurrency.

**Walk-in queues.** Named lines customers join without an appointment.
Each queue has:

- a staff console — call next, issue a ticket at the desk, and move
  tickets through `waiting -> called -> serving -> done`, refreshing on a
  timer so several counters see each other's work;
- a public board at `/q/{org}/{queue}` — what is being served now, how
  long the line is, and a form to join it;
- a per-customer ticket page showing their place in line and an estimated
  wait drawn from the queue's own service history, with a "leave the line"
  button while they are still waiting or being called — a customer who
  walks out silently is otherwise called to an empty room and written off
  as a no-show, inflating the wait shown to everyone behind them;
- a waiting-room display at `/q/{org}/{queue}/display`, showing numbers
  only — never names, since a whole room reads it;
- a day report at `/app/queues/{id}/report`, answering "how did yesterday
  go": totals, how long people waited before being called, how long each
  took at the counter, and an hour-by-hour histogram of when they arrived.
  Rates are over resolved tickets rather than issued ones, so a queue
  mid-morning is not flattered by outcomes nobody knows yet. Admins can
  download the same day as CSV.

Ticket numbers restart at 1 each service day, in the org's timezone, and
are allocated so that simultaneous walk-ins can never share a number.
"Call next" claims a ticket with `FOR UPDATE SKIP LOCKED`, so two staff
pressing it at the same moment are handed two different customers.

Everything is multi-tenant: `org_id` scopes every read and write, and the
tenant comes from the session (authenticated pages) or from the URL slug
(public pages) — never from a client-supplied field.

**Roles.** Admins configure the organisation: services, staff, queues,
settings, and the CSV export, which carries customer names and phone
numbers. Staff do the job: the schedule, every queue console action, and
the day report. The split is enforced on the routes and covered by tests
that sign in as each role.

**Settings.** `/app/settings` sets the organisation's name and timezone.
The timezone is the one every service day is measured in — when ticket
numbers restart, and what a report's "today" covers — so it is validated
against the runtime's zone database rather than trusted; a typo would
otherwise silently fall back to UTC. The public slug stays fixed across a
rename, so a printed QR code keeps working.

## Status

Under active scaffold development, following a 14-day build plan (see
commit history for day-by-day progress). Auth, RBAC, CSRF, tenant
isolation, the appointment flow and the walk-in queue flow are all in
place and verified against a real Postgres instance.

## Operating it

- `GET /healthz` — readiness, including a database ping, so an
  orchestrator does not route traffic to an instance that cannot reach
  Postgres.
- `GET /version` — the commit this binary was built from, whether that
  tree was clean, and the Go version. Unauthenticated, because a deploy
  pipeline and an uptime check both read it without credentials. The same
  line opens the startup log, and `queuepilot version` prints it.
- Every request gets an ID, reused from `X-Request-Id` when a proxy
  already set one and echoed back in the response, so a user reporting
  "it broke at 11:40" can quote a value that appears verbatim in the
  logs. One access line is written per request, after the handler, so it
  carries the outcome; polling endpoints drop to debug level, since the
  board and display re-fetch themselves by design and at info they bury
  every line that says something happened.
- Expired sessions are swept every six hours. Expiry itself is enforced
  at lookup, so this is housekeeping — without it the table grows by one
  dead row per login, forever.
- Security headers are set on every response, including error pages. The
  content security policy allows no inline or eval'd script; the one
  concession is inline *styles*, which the report histogram's bar widths
  need. Framing is denied everywhere except the waiting-room display,
  which is meant to run inside signage.

## Prerequisites

- Go 1.23+
- A Postgres database. Either works:
  - **Hosted (current dev setup)**: a [Supabase](https://supabase.com)
    project's Postgres connection string. Use the direct or session-mode
    pooler connection (port 5432), not the transaction-mode pooler
    (6543) — the app uses prepared statements. Set it as `DATABASE_URL`
    in `.env`.
  - **Local via Docker**: `docker compose up -d db` using the bundled
    `docker-compose.yml`. On macOS without Docker Desktop,
    [Colima](https://github.com/abiosoft/colima) works too:
    `brew install colima docker docker-compose && colima start`.
- [`migrate`](https://github.com/golang-migrate/migrate) CLI:
  `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
- [`sqlc`](https://sqlc.dev): `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- [`templ`](https://templ.guide): `go install github.com/a-h/templ/cmd/templ@latest`
- Tailwind CLI (standalone, no Node required): `./scripts/install-tailwind.sh`

## Local development

```sh
cp .env.example .env         # first time only; then set DATABASE_URL
make migrate-up              # applies migrations
make templ-generate          # generates Go from .templ files
make tailwind-build          # builds internal/web/static/css/app.css
make run                     # starts the server on :8080
curl localhost:8080/healthz  # -> ok
```

## Demo data

```sh
make seed
```

Creates one worked-through organization — an admin, two staff on two
services with weekday hours, and a walk-in queue partway through being
served — then prints the sign-in details and the console, board and
display URLs. It refuses to run with `ENV=production`, since it writes
fabricated customers and accounts whose password is in the source.

## Testing

```sh
make test              # unit tests
make test-integration  # integration tests against a real Postgres
```

Unit tests cover the pure logic — slot generation, the appointment and
ticket lifecycles, wait estimation, rate limiting, report aggregation,
CSV formatting, timezone validation — with no database. Integration tests
cover what only a real database can prove: the double-booking constraint,
concurrent ticket numbering, concurrent "call next", that two staff
changing one ticket's status at the same instant produce exactly one
winner, and that one org cannot read or modify another's data.

`make check` runs everything CI does except the integration tests,
including the generated-artifact drift checks. Run it before pushing.

## Deployment notes

- `TRUST_PROXY_HEADERS=true` is required when the app runs behind a proxy
  that overwrites `X-Forwarded-For` (Fly.io, a load balancer, nginx), so
  rate limiting identifies the real client. Leave it false otherwise: a
  header the client sets is a header the client can vary, which would
  defeat the limit entirely.
- Rate limiting is per-process, so N instances allow N times the
  configured rate. That is a deliberate tradeoff for a walk-in queue; see
  `internal/platform/ratelimit`.
- `internal/web/static/css/app.css` is a committed build artifact. Run
  `make tailwind-build` after touching any template, or CI will fail on
  the drift check.
- Static assets are embedded in the binary, so it can be run from any
  working directory. Rebuild after changing one — a stale binary serves
  the stale asset.

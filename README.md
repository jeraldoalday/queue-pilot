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

Customers book themselves at `/book/{org}`: the services on offer, a
day's free times, and a confirmation page they can come back to and
cancel from. The times are gathered per staff member and merged, so two
colleagues free at 10:00 is one 10:00 on the page rather than a choice
the customer has no way to make; which of them takes it is decided by
name, so the page cannot quietly reassign a slot between rendering and
submitting. A booking has to start at least 15 minutes out and within 60
days, and the submitted slot is re-derived and matched rather than
trusted — an edited form asking for three in the morning, or for a time
that has since been taken, comes back as the picker with a sentence.
Cancelling frees the slot for the next person, which is the whole reason
it is offered: a customer who simply does not come is written off as a
no-show and their time stays blocked.

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

Each queue also answers as JSON at `/api/q/{org}/{queue}`, for a screen
this app does not render — a lobby television running someone else's
signage, or a business putting its own waiting time on its own website.
It carries no names or phone numbers, nothing the waiting-room display
would not show a whole room, which is what makes it safe to serve
without a session and from any origin. The wait it reports comes from
the same calculation the public board renders, so a website cannot
contradict the sign above the door.

Ticket numbers restart at 1 each service day, in the org's timezone, and
are allocated so that simultaneous walk-ins can never share a number.
"Call next" claims a ticket with `FOR UPDATE SKIP LOCKED`, so two staff
pressing it at the same moment are handed two different customers.

Everything is multi-tenant: `org_id` scopes every read and write, and the
tenant comes from the session (authenticated pages) or from the URL slug
(public pages) — never from a client-supplied field.

**Dashboard.** `/app` opens on the floor rather than on a welcome
message: how many people are waiting across every line, how many are at
a counter, how many have been served today, which queue is longest, and
the next appointment still ahead — measured from now, so one that
started twenty minutes ago is not offered as who is coming through the
door next. It also carries the org's public booking address, or says
what is missing when nothing is bookable yet.

**Roles.** Admins configure the organisation: services, staff, queues,
settings, and the CSV export, which carries customer names and phone
numbers. Staff do the job: the schedule, every queue console action, and
the day report. The split is enforced on the routes and covered by tests
that sign in as each role.

An admin can suspend a staff account, which signs it out everywhere and
blocks it from signing back in. Suspension rather than deletion:
appointments record who performed them and tickets record who called
them, so removing the row would either destroy that history or be
refused by the keys holding it. The query only matches staff rows, so
the route cannot reach an admin — an organisation cannot lock itself out
of its own account, by accident or through a hand-edited form.

**Settings.** `/app/settings` sets the organisation's name and timezone.
The timezone is the one every service day is measured in — when ticket
numbers restart, and what a report's "today" covers — so it is validated
against the runtime's zone database rather than trusted; a typo would
otherwise silently fall back to UTC. The public slug stays fixed across a
rename, so a printed QR code keeps working.

**A React front end.** `web/` is the customer's half of the product as a
single-page app: the same board, ticket, waiting-room display, service
list, booking flow and confirmation page, reading `/api/v1` and nothing
else. It is additive — the server-rendered pages under `/q` and `/book`
are unchanged and remain the ones a printed QR code should point at,
because they need no build step and work with scripting switched off. The
React app is for the cases those cannot serve: a business embedding
booking in its own site, a lobby screen running someone else's shell, or
anywhere the interaction wants to be richer than a form post.

Three things about it are worth knowing before reading the code. The wait
estimate is never recomputed on the client — `queue.Estimate` decides it
and the API sends whole minutes, so the number on a phone and the number
on the sign above the door come from one implementation. Every instant on
the wire carries the organisation's own offset, and the front end renders
the wall clock that offset names rather than converting it, so a clinic's
10:00 is 10:00 to a customer reading from another continent. And the
board never blanks while it refreshes: a failed poll leaves the last good
numbers up with a note, because a screen that goes blank reads to a room
as the queue being broken.

## Status

Under active scaffold development, following a 14-day build plan (see
commit history for day-by-day progress). Auth, RBAC, CSRF, tenant
isolation, the appointment flow (staff-booked and customer-booked), the
walk-in queue flow and the org dashboard are all in place and verified
against a real Postgres instance.

## The JSON API

Everything a customer can do, as JSON under `/api/v1`. It follows the
same rules the public pages do: the tenant is the `{org}` path segment
and nothing else, a missing org and another business's queue answer 404
identically so the URL space cannot be read as a customer list, the two
writes share the public forms' rate limit, and reads are unlimited
because the pages that call them poll by design.

```
GET  /api/v1/orgs/{org}                                  name, services, open lines
GET  /api/v1/orgs/{org}/queues/{queue}                   one board, numbers included
POST /api/v1/orgs/{org}/queues/{queue}/tickets           take a number
GET  /api/v1/orgs/{org}/queues/{queue}/tickets/{id}      one customer's own number
POST /api/v1/orgs/{org}/queues/{queue}/tickets/{id}/cancel   leave the line
GET  /api/v1/orgs/{org}/services/{service}/slots?date=   the day's open times
POST /api/v1/orgs/{org}/services/{service}/bookings      book one
GET  /api/v1/orgs/{org}/appointments/{id}                one customer's own booking
GET  /api/v1/orgs/{org}/appointments/{id}/calendar.ics   the same booking, as a calendar file
POST /api/v1/orgs/{org}/appointments/{id}/cancel         release it
```

Failures all take one shape — `{"error": {"code", "message"}}` — with a
closed set of codes (`not_found`, `validation`, `queue_closed`,
`slot_taken`, `too_soon`, `too_far_ahead`, `not_cancellable`,
`rate_limited`, `internal`). The code is for a front end to branch on and
the message is for a person to read, which is what lets a UI say "that
time was just taken, here is what is left" without matching on English
prose. An unknown path under `/api/v1/` answers with that envelope too,
rather than falling through to an HTML page a JSON parser then misreports
— and so does a request the rate limiter refuses, which is middleware
rather than a handler and used to arrive as plain text with only its
status to identify it.

The one exception to "everything is JSON" is `calendar.ics`, which
answers `text/calendar` because the point of that response is that it can
be handed straight to the operating system. A front end could assemble
the file itself, and that is exactly what this avoids: a second
implementation of iCalendar's escaping and folding rules is how the two
ends of a product come to disagree about what a customer's appointment
says. Its failures still take the envelope's shape.

Preflights are answered, so a cross-origin JSON `POST` works. Credentials
are never allowed: no endpoint here reads a session, so "any origin"
cannot mean "any origin acting as the signed-in user". `Content-Disposition`
is exposed, because a browser otherwise hides the download's filename from
the page that asked for it and every appointment saves under one invented
name.

## Operating it

- `GET /api/q/{org}/{queue}` — one queue's live state as JSON, described
  above. Unauthenticated, cacheable for five seconds, and served with
  `Access-Control-Allow-Origin: *` because a business embedding its own
  waiting time in its own site should not need a proxy to do it. It is
  the original published payload and is kept exactly as it was — the
  `/api/v1` board nests it rather than reshaping it, because businesses
  have embedded this URL and breaking it to tidy a route is not a trade
  this app gets to make.
- `GET /metrics` — a request counter and a duration histogram per route,
  method and status, plus the build and uptime, in Prometheus' text
  format. It is off unless `METRICS_TOKEN` is set, and a request without
  that bearer token answers 404 rather than 401: request counts per route
  describe every tenant's day at once, and whether a deployment collects
  them is not a stranger's business either. The route label is a template
  (`/q/:org/:queue`), never a path — a series exists per distinct label
  combination, so counting real paths would mint one per queue per org,
  driven by strangers, growing until the process restarts.
- `GET /robots.txt`, and `X-Robots-Tag: noindex` on every response but
  the handful of pages a business wants found. A customer's ticket and
  confirmation URLs are capabilities — holding one is permission to read
  the page and cancel from it — and a crawler is a machine for copying
  URLs somewhere else. The rule is closed by default, so a route added
  later is private until somebody decides otherwise.
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

- Go 1.26+ (the toolchain `go.mod` pins)
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
services with weekday hours, a walk-in queue partway through being
served, and appointments booked into the next free slots — then prints
the sign-in details and the console, board, display, JSON and online
booking URLs. It refuses to run with `ENV=production`, since it writes
fabricated customers and accounts whose password is in the source.

## Testing

```sh
make test              # unit tests
make test-integration  # integration tests against a real Postgres
```

Unit tests cover the pure logic — slot generation, the appointment and
ticket lifecycles, wait estimation, the public booking window and the
slot merge behind it, rate limiting, report aggregation, the dashboard's
totals, CSV and JSON formatting, timezone validation — with no database. Integration tests
cover what only a real database can prove: the double-booking constraint,
concurrent ticket numbering, concurrent "call next", that two staff
changing one ticket's status at the same instant produce exactly one
winner, that a suspended account loses its session and cannot sign back
in, that a service with appointments against it refuses to be deleted,
that the whole public booking flow works through the real router — the
tests read the slot values off the rendered page and submit those — and
that one org cannot read or modify another's data.

`make check` runs everything CI's Go job does except the integration
tests, including the generated-artifact drift checks. Run it before
pushing.

The iCalendar writer is tested against what importers are unforgiving
about rather than against a golden file: CRLF breaks, folding at 75
octets without splitting a UTF-8 rune, escaped TEXT values, and a value
carrying a newline being unable to end its own line and start a property
of its choosing — customers type their own names, so that last one is
reachable.

### The front end

```sh
make web-install   # npm ci
make run           # in one terminal: the Go server on :8080
make web-dev       # in another: Vite on :5173, proxying /api to :8080
make web-test      # Vitest — units, components, hooks
make web-e2e       # Playwright — the customer journeys, in a browser
make web-check     # typecheck + Vitest + build, the front-end half of CI
```

The dev server proxies `/api` rather than pointing at `localhost:8080`,
so development is same-origin like the intended deployment. Developing
against a cross-origin setup means every CORS mistake is found in
production instead.

Vitest covers the pure logic (wait wording, timezone handling, the client's
status mapping and abort handling), the two hooks every page is built from,
and each page against fixtures shaped like the real payloads. The wait
table mirrors `internal/queue/wait_test.go` case for case, so the HTML
board and the React one say the same sentence.

Playwright drives the built app in Chromium and on a phone viewport, with
the API answered at the network boundary — no database, no server. It
covers what the Go tests cannot reach: getting from a QR code to a number
in hand, the board following the queue without a reload, the back button
not taking a second number, a slot lost mid-form being recoverable, the
whole booking flow read from a browser set to another timezone, and the
calendar download arriving as a real file under the name the API asked
for.

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
- `web/dist` is a static bundle. Serve it from the same hostname as the
  API and it needs no configuration at all — that is what the relative
  `/api/v1` paths are for, and it is why the dev server proxies rather
  than pointing at a port. Hosting it elsewhere works (the API allows any
  origin and answers preflights) and needs `VITE_API_BASE_URL` set at
  build time, but a hardcoded origin is the thing that breaks the day a
  business puts the app on its own domain.

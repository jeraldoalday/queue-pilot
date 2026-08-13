# QueuePilot

Appointment booking and walk-in queue management, built as a multi-tenant
Go service. Server-rendered with [templ](https://templ.guide) + htmx +
Tailwind CSS — one binary, no Node/npm build step.

## Status

Under active scaffold development, following a 14-day build plan (see task
tracker / commit history for day-by-day progress). Through Day 4: project
scaffold, schema/migrations, auth (signup/login/sessions), RBAC, CSRF, and
the cross-tenant isolation guarantee — all verified against a real
Postgres instance.

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

## Testing

```sh
make test              # unit tests
make test-integration  # integration tests against a real Postgres
```

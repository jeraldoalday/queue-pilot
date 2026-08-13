# QueuePilot

Appointment booking and walk-in queue management, built as a multi-tenant
Go service. Server-rendered with [templ](https://templ.guide) + htmx +
Tailwind CSS — one binary, no Node/npm build step.

## Status

Under active scaffold development. See `PLAN.md` (not committed — internal
build plan) for the day-by-day build sequence. Currently: Day 1 (project
scaffold, health check).

## Prerequisites

- Go 1.23+
- Docker + Docker Compose (for local Postgres) — this repo assumes the
  `docker` and `docker compose` CLIs are on `PATH`. On macOS without
  Docker Desktop, [Colima](https://github.com/abiosoft/colima) works too:
  `brew install colima docker docker-compose && colima start`.
- [`migrate`](https://github.com/golang-migrate/migrate) CLI:
  `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
- [`sqlc`](https://sqlc.dev): `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- [`templ`](https://templ.guide): `go install github.com/a-h/templ/cmd/templ@latest`
- Tailwind CLI (standalone, no Node required): `./scripts/install-tailwind.sh`

## Local development

```sh
cp .env.example .env        # first time only
make docker-up               # starts Postgres in Docker
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

.PHONY: run dev docker-up docker-down migrate-up migrate-down migrate-create \
	sqlc-generate templ-generate tailwind-build tailwind-watch test test-integration seed \
	generate check check-all version build fmt \
	web-install web-dev web-test web-e2e web-build web-check

-include .env
export

run:
	go run ./cmd/queuepilot

dev:
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "air not installed, falling back to plain go run (no hot reload)"; \
		go run ./cmd/queuepilot; \
	fi

docker-up:
	docker compose up -d db

docker-down:
	docker compose down

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1

migrate-create:
	migrate create -ext sql -dir migrations -seq $(name)

sqlc-generate:
	sqlc generate

templ-generate:
	templ generate ./...

tailwind-build:
	./scripts/tailwindcss -i internal/web/static/css/input.css -o internal/web/static/css/app.css --minify

tailwind-watch:
	./scripts/tailwindcss -i internal/web/static/css/input.css -o internal/web/static/css/app.css --watch

# generate rebuilds every committed artifact CI checks for drift. Run it
# after touching a .templ file, a .sql query, or anything a template
# styles — all three are generated into files that live in git.
generate: templ-generate sqlc-generate tailwind-build

# fmt formats every Go file in place. It is what to run when `check` or CI
# reports a file as unformatted — both of those only ever report, since a
# check that rewrites the tree it is checking is not a check.
fmt:
	gofmt -w .

# check is what CI runs, minus the integration tests that need a database.
check: generate
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	go build ./...
	go test ./... -race -short

test:
	go test ./... -race -short

# check-all is check plus the front end and the integration tests. The
# latter need DATABASE_URL to point at a migrated database. It is what to
# run before pushing anything that touches a query, a transaction or a
# constraint — the guarantees those carry (no double-booking, no duplicate
# ticket numbers, one winner per status change, no cross-tenant reads) are
# only provable against a real Postgres.
check-all: check web-check test-integration

# version prints the commit a build from this tree would identify itself
# as — the same string /version serves and the startup log opens with.
#
# It builds first rather than using `go run`, which does not stamp the VCS
# revision into what it runs and so reports "unknown" no matter what the
# tree contains. /bin is already gitignored.
version: build
	./bin/queuepilot version

build:
	go build -o bin/queuepilot ./cmd/queuepilot

test-integration:
	go test ./... -race -run Integration -tags=integration

# ---------------------------------------------------------------------------
# The React front end, in web/.
#
# It talks to /api/v1 and nothing else, so it can be developed against a
# running `make run` and deployed as static files behind the same hostname.
# Its dev server proxies /api to :8080 so development is same-origin like
# the intended deployment — see web/vite.config.ts.
# ---------------------------------------------------------------------------

web-install:
	cd web && npm ci

# web-dev needs `make run` in another terminal: the proxy has to have
# something to proxy to.
web-dev:
	cd web && npm run dev

web-test:
	cd web && npm run test

# The end-to-end tests answer the API in the browser, so this needs no
# database and no server — only the browsers, which npx installs on first
# run.
web-e2e:
	cd web && npm run test:e2e

web-build:
	cd web && npm run build

# web-check is the front-end half of what CI runs.
web-check:
	cd web && npm run check

seed:
	go run ./cmd/queuepilot seed

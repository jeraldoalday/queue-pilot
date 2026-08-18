.PHONY: run dev docker-up docker-down migrate-up migrate-down migrate-create \
	sqlc-generate templ-generate tailwind-build tailwind-watch test test-integration seed \
	generate check

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

# check is what CI runs, minus the integration tests that need a database.
check: generate
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	go build ./...
	go test ./... -race -short

test:
	go test ./... -race -short

test-integration:
	go test ./... -race -run Integration -tags=integration

seed:
	go run ./cmd/queuepilot seed

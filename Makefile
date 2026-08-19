DATABASE_URL ?= postgres://trivial:trivial@localhost:5432/trivial?sslmode=disable
TEST_DATABASE_URL ?= postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable

.PHONY: db-up db-down migrate seed test lint fmt web-build serve

db-up:
	docker compose up -d db testdb
	docker compose exec -T db sh -c 'until pg_isready -U trivial -q; do sleep 0.5; done'
	docker compose exec -T testdb sh -c 'until pg_isready -U trivial -q; do sleep 0.5; done'

db-down:
	docker compose down

migrate: db-up
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/trivial migrate up

seed: migrate
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/trivial seed apply

test: db-up
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./... -count=1

fmt:
	gofmt -w .

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

web-build:
	cd web && npm run build

serve: web-build migrate
	DATABASE_URL="$(DATABASE_URL)" COOKIE_SECURE=false go run ./cmd/trivial serve

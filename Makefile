DATABASE_URL ?= postgres://trivial:trivial@localhost:5432/trivial?sslmode=disable
TEST_DATABASE_URL ?= postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable
DEV_QUESTION_COOLDOWN_DAYS ?= 7

.PHONY: db-up db-down migrate seed replace-seed test lint fmt web-build serve

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

replace-seed: migrate
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/trivial seed replace --file question_dump.json
	DATABASE_URL="$(DATABASE_URL)" QUESTION_COOLDOWN_DAYS="$(DEV_QUESTION_COOLDOWN_DAYS)" go run ./cmd/trivial puzzles generate --days 1

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
	DATABASE_URL="$(DATABASE_URL)" COOKIE_SECURE=false DEVELOPMENT_MODE=true go run ./cmd/trivial serve

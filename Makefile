DATABASE_URL ?= postgres://trivial:trivial@localhost:5432/trivial?sslmode=disable
TEST_DATABASE_URL ?= postgres://trivial:trivial@localhost:5433/trivial_test?sslmode=disable
# A fixed development signing key, so restarting the server does not sign
# everyone out. Production must supply its own; see .env.example.
APP_SECRET ?= dev-secret-not-for-production-0123456789
DEV_QUESTION_COOLDOWN_DAYS ?= 7
HTTP_ADDRESS ?= :8080
BIN ?= trivial

.PHONY: db-up db-down migrate seed replace-seed test lint fmt web-build build serve stop restart deploy deploy-status rollback

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

# Compile the Vue bundle and the Go binary without starting anything.
build: web-build
	go build -o $(BIN) ./cmd/trivial

serve: web-build migrate
	DATABASE_URL="$(DATABASE_URL)" HTTP_ADDRESS="$(HTTP_ADDRESS)" APP_SECRET="$(APP_SECRET)" COOKIE_SECURE=false DEVELOPMENT_MODE=true go run ./cmd/trivial serve

# `go run` leaves a compiled child process that outlives its parent and keeps
# the port, so kill the wrapper and the binary. Match the binary on its `serve`
# argument rather than its path: go runs it from the build cache
# (~/Library/Caches/go-build/...) or a temp dir (.../b001/exe/) depending on
# whether the build was already cached, so the path is not stable. Bracketed
# patterns keep pkill from matching this recipe's own shell, and `|| true` keeps
# "nothing was running" a success.
stop:
	@pkill -f '[g]o run ./cmd/trivial' || true
	@pkill -f '[/]trivial serve' || true
	@echo "trivial server stopped"

restart: stop serve

# Deploy to this box: build, test, swap the binary in and restart, rolling back
# automatically if the new one does not answer /healthz. Unlike `serve`, this
# reads .env rather than the development defaults above, so it deploys the real
# configuration. See scripts/deploy.sh --help.
deploy:
	./scripts/deploy.sh

deploy-status:
	@./scripts/deploy.sh --status

rollback:
	./scripts/deploy.sh --rollback

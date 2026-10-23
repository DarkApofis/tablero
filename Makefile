-include .env
export

MIGRATIONS_DIR := backend/migrations
GOOSE := goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)"
TEST_DATABASE_URL ?= postgres://tablero:tablero@localhost:5432/tablero_test?sslmode=disable

.PHONY: migrate-up migrate-down migrate-status test-db test

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-status:
	$(GOOSE) status

# Creates (if missing) and migrates the database used by integration tests.
test-db:
	docker compose exec -T db psql -U tablero -d tablero -tAc "SELECT 1 FROM pg_database WHERE datname = 'tablero_test'" | grep -q 1 \
		|| docker compose exec -T db createdb -U tablero tablero_test
	goose -dir $(MIGRATIONS_DIR) postgres "$(TEST_DATABASE_URL)" up

# Integration tests run against TEST_DATABASE_URL; without it they are skipped.
test: test-db
	cd backend && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./...

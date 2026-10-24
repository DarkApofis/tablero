-include .env
export

MIGRATIONS_DIR := backend/migrations
GOOSE := goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)"
TEST_DATABASE_URL_LOCAL := postgres://tablero:tablero@localhost:5432/tablero_test?sslmode=disable

.PHONY: migrate-up migrate-down migrate-status test-db test

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-status:
	$(GOOSE) status

# Optional: a long-lived test database. Export TEST_DATABASE_URL to make the
# integration tests use it instead of starting a Testcontainers Postgres.
test-db:
	docker compose exec -T db psql -U tablero -d tablero -tAc "SELECT 1 FROM pg_database WHERE datname = 'tablero_test'" | grep -q 1 \
		|| docker compose exec -T db createdb -U tablero tablero_test
	goose -dir $(MIGRATIONS_DIR) postgres "$(TEST_DATABASE_URL_LOCAL)" up

# Integration tests start a throwaway Postgres with Testcontainers (needs
# Docker). `go test -short ./...` runs only the unit tests.
test:
	cd backend && go test ./...

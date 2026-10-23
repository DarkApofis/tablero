-include .env
export

MIGRATIONS_DIR := backend/migrations
GOOSE := goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)"

.PHONY: migrate-up migrate-down migrate-status

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-status:
	$(GOOSE) status

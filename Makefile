SHELL := /usr/bin/env bash
.SHELLFLAGS := -Eeuo pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := $(CURDIR)/bin
GOOSE := $(BIN_DIR)/goose
SQLC := $(BIN_DIR)/sqlc
TEMPL := $(BIN_DIR)/templ
AIR := $(BIN_DIR)/air
TERRAFORM_VERSION := 1.15.8
TERRAFORM_IMAGE := hashicorp/terraform:$(TERRAFORM_VERSION)

.PHONY: help tools dev-infra dev-infra-down dev-infra-clean generate generate-fast migrate-up migrate-down-one migrate-status dev-bootstrap dev test test-integration test-e2e terraform-fmt terraform-validate terraform-check verify verify-foundation reset-local fmt-check

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install pinned Go development tools into ./bin
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/a-h/templ/cmd/templ@v0.3.1020
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
	GOBIN=$(BIN_DIR) go install github.com/pressly/goose/v3/cmd/goose@latest
	GOBIN=$(BIN_DIR) go install github.com/air-verse/air@v1.67.1

dev-infra: ## Start local PostgreSQL and MinIO
	docker compose up -d --wait postgres minio
	docker compose run --rm minio-init

dev-infra-down: ## Stop local services without deleting data
	docker compose down

dev-infra-clean: ## Delete local services and all local data
	@if [[ "$${CI:-}" != "true" ]]; then read -r -p "Delete all local MyCFC data? [y/N] " answer; [[ $$answer == [yY] ]]; fi
	docker compose down -v

generate: ## Generate templ/sqlc output and production browser assets
	$(TEMPL) generate
	$(SQLC) generate
	npm ci
	npm run build

generate-fast: ## Regenerate source and assets for Air
	$(TEMPL) generate
	$(SQLC) generate
	npm run build

migrate-up: ## Apply all pending local database migrations
	@set -a; source .env; set +a; $(GOOSE) -dir internal/db/migrations postgres "$$DATABASE_URL" up

migrate-down-one: ## Roll back one migration outside production
	@set -a; source .env; set +a; [[ "$${APP_ENV}" != "production" ]] || { echo "down migrations are forbidden in production" >&2; exit 1; }; $(GOOSE) -dir internal/db/migrations postgres "$$DATABASE_URL" down

migrate-status: ## Show migration status
	@set -a; source .env; set +a; $(GOOSE) -dir internal/db/migrations postgres "$$DATABASE_URL" status

dev-bootstrap: ## Create .env, start infrastructure, migrate and build assets
	./scripts/local-bootstrap.sh

dev: ## Run the application through Air (never go run)
	@set -a; source .env; set +a; exec $(AIR) -c .air.toml

test: ## Run unit tests
	go test ./internal/... ./cmd/...

test-integration: dev-infra ## Run integration tests against local services
	@set -a; source .env; set +a; if ! docker compose exec -T postgres psql -U "$${POSTGRES_USER}" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='mycfc_test'" | grep -qx 1; then docker compose exec -T postgres createdb -U "$${POSTGRES_USER}" mycfc_test; fi; TEST_DATABASE_URL="postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@localhost:5432/mycfc_test?sslmode=disable"; $(GOOSE) -dir internal/db/migrations postgres "$$TEST_DATABASE_URL" up; TEST_DATABASE_URL="$$TEST_DATABASE_URL" go test -tags=integration ./internal/db/... ./internal/handlers/... ./internal/storage/...

test-e2e: dev-bootstrap ## Run browser and accessibility tests
	docker compose --profile e2e up --force-recreate --abort-on-container-exit --exit-code-from e2e e2e-app e2e

terraform-fmt: ## Check Terraform formatting through the pinned container
	docker run --rm --user "$$(id -u):$$(id -g)" -v "$(CURDIR):/workspace" -w /workspace $(TERRAFORM_IMAGE) fmt -check -recursive infra

terraform-validate: ## Validate Terraform stacks through the pinned container without remote state
	docker run --rm --user "$$(id -u):$$(id -g)" --entrypoint /bin/sh -v "$(CURDIR):/workspace" -w /workspace $(TERRAFORM_IMAGE) -ec 'terraform -chdir=infra/bootstrap init -backend=false && terraform -chdir=infra/bootstrap validate && terraform -chdir=infra/environments/production init -backend=false && terraform -chdir=infra/environments/production validate'

terraform-check: terraform-fmt terraform-validate ## Run all containerized Terraform checks

fmt-check: ## Check Go formatting
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './internal/db/generated/*'))" || { gofmt -l $$(find . -name '*.go' -not -path './internal/db/generated/*'); exit 1; }

verify-foundation: fmt-check ## Verify the currently implemented foundation slice
	go vet ./internal/config/... ./internal/httpx/... ./internal/locale/... ./internal/storage/... ./internal/validation/...
	go test ./internal/config/... ./internal/httpx/... ./internal/locale/... ./internal/storage/... ./internal/validation/...
	npm ci
	npm run build

verify: verify-foundation ## Run the full production gate; expected to fail until all slices are complete
	$(SQLC) compile
	$(SQLC) generate
	$(TEMPL) generate
	go vet ./...
	go test ./...
	npm run test:e2e
	@test -d infra/environments/production/.terraform || { echo "Terraform production stack is not implemented" >&2; exit 1; }

reset-local: dev-infra-clean dev-bootstrap ## Delete and recreate the local environment

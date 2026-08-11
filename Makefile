SHELL := /usr/bin/env bash
.SHELLFLAGS := -Eeuo pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := $(CURDIR)/bin
SQLC := $(BIN_DIR)/sqlc
TEMPL := $(BIN_DIR)/templ
AIR := $(BIN_DIR)/air
TERRAFORM_VERSION := 1.15.8
TERRAFORM_IMAGE := hashicorp/terraform:$(TERRAFORM_VERSION)

.PHONY: help tools dev-infra dev-infra-down dev-infra-clean generate generate-fast db-provision db-provision-test dev-bootstrap dev ui-review-reset ui-review-dev ui-review-screenshots test test-deployment test-integration test-e2e test-e2e-ci terraform-fmt terraform-validate terraform-check verify verify-foundation reset-local fmt-check

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install pinned Go development tools into ./bin
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/a-h/templ/cmd/templ@v0.3.1020
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
	GOBIN=$(BIN_DIR) go install github.com/air-verse/air@v1.67.1

dev-infra: ## Start local PostgreSQL, MinIO, and Mailpit
	docker compose up -d --wait postgres minio mailpit
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

db-provision: ## Provision the reset-only baseline into an empty local database
	@set -a; source .env; set +a; if docker compose exec -T postgres psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -tAc "SELECT to_regclass('public.users') IS NOT NULL" | grep -qx t; then echo "local database already has the baseline; use make reset-local to recreate it"; else docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" < internal/db/schema.sql; fi

db-provision-test: ## Recreate and provision the local integration-test database
	@set -a; source .env; set +a; docker compose exec -T postgres dropdb -U "$$POSTGRES_USER" --if-exists mycfc_test; docker compose exec -T postgres createdb -U "$$POSTGRES_USER" mycfc_test; docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d mycfc_test < internal/db/schema.sql

dev-bootstrap: ## Create .env, start infrastructure, migrate and build assets
	./scripts/local-bootstrap.sh

dev: ## Run the application through Air (never go run)
	@set -a; source .env; set +a; exec $(AIR) -c .air.toml

ui-review-reset: ## Recreate the isolated UI-review database and deterministic personas
	./scripts/ui-review-reset.sh

ui-review-dev: ui-review-reset ## Run the UI-review dataset through Air
	@set -a; source .env; set +a; DATABASE_URL="postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@localhost:5432/mycfc_ui_review?sslmode=disable" exec $(AIR) -c .air.toml

ui-review-screenshots: ui-review-reset ## Capture deterministic desktop/mobile UI-review screenshots
	docker compose --profile ui-review up --force-recreate --abort-on-container-exit --exit-code-from ui-review-capture ui-review-app ui-review-capture

test: ## Run unit tests
	go test ./internal/... ./cmd/...

test-deployment: ## Run production release orchestration tests
	sh deployment/pull-release_test.sh
	sh deployment/release-status_test.sh

test-integration: dev-infra db-provision-test ## Run integration tests against local services
	@set -a; source .env; set +a; TEST_DATABASE_URL="postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@localhost:5432/mycfc_test?sslmode=disable" go test -tags=integration ./internal/db/... ./internal/handlers/... ./internal/storage/...

test-e2e: dev-bootstrap ## Run browser and accessibility tests
	docker compose --profile e2e up --force-recreate --abort-on-container-exit --exit-code-from e2e e2e-app e2e

test-e2e-ci: ## Run the lean CI browser and accessibility gate
	./scripts/e2e-ci-bootstrap.sh
	docker compose -f compose.yaml -f compose.e2e-ci.yaml --profile e2e up --force-recreate --abort-on-container-exit --exit-code-from e2e e2e-app e2e

terraform-fmt: ## Check Terraform formatting through the pinned container
	docker run --rm --user "$$(id -u):$$(id -g)" -v "$(CURDIR):/workspace" -w /workspace $(TERRAFORM_IMAGE) fmt -check -recursive infra

terraform-validate: ## Validate Terraform stacks through the pinned container without remote state
	docker run --rm --user "$$(id -u):$$(id -g)" --entrypoint /bin/sh -v "$(CURDIR):/workspace" -w /workspace $(TERRAFORM_IMAGE) -ec 'TF_DATA_DIR=/tmp/mycfc-bootstrap terraform -chdir=infra/bootstrap init -backend=false && TF_DATA_DIR=/tmp/mycfc-bootstrap terraform -chdir=infra/bootstrap validate && TF_DATA_DIR=/tmp/mycfc-production terraform -chdir=infra/environments/production init -backend=false && TF_DATA_DIR=/tmp/mycfc-production terraform -chdir=infra/environments/production validate'

terraform-check: terraform-fmt terraform-validate ## Run all containerized Terraform checks

fmt-check: ## Check Go formatting
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './internal/db/generated/*'))" || { gofmt -l $$(find . -name '*.go' -not -path './internal/db/generated/*'); exit 1; }

verify-foundation: fmt-check test-deployment ## Run fast focused checks and build browser assets
	go vet ./internal/config/... ./internal/httpx/... ./internal/locale/... ./internal/storage/... ./internal/validation/...
	go test ./internal/config/... ./internal/httpx/... ./internal/locale/... ./internal/storage/... ./internal/validation/...
	npm ci
	npm run build

verify: verify-foundation terraform-check ## Run the full source verification gate
	$(SQLC) compile
	$(SQLC) generate
	$(TEMPL) generate
	go vet ./...
	go test ./...
	npm run test:e2e

reset-local: dev-infra-clean dev-bootstrap ## Delete and recreate the local environment

SHELL := /usr/bin/env bash
.SHELLFLAGS := -Eeuo pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := $(CURDIR)/bin
SQLC := $(BIN_DIR)/sqlc
TEMPL := $(BIN_DIR)/templ
AIR := $(BIN_DIR)/air
STATICCHECK := $(BIN_DIR)/staticcheck
ACTIONLINT := $(BIN_DIR)/actionlint
TERRAFORM_VERSION := 1.15.8
TERRAFORM_IMAGE := hashicorp/terraform:$(TERRAFORM_VERSION)
SHELLCHECK_IMAGE := koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d
HADOLINT_IMAGE := hadolint/hadolint:v2.15.1-alpine@sha256:a1d49ae1a4e83c1dbad26b8c1ad7588c8bd1e04f4866b34ad3cac50335198552
TFLINT_IMAGE := ghcr.io/terraform-linters/tflint:v0.64.0@sha256:1c595f42d794c32c45a6ea8b58655fd66433d4ca3b1bc631c574a48d120bd19f
TERRAFORM_PLUGIN_CACHE_DIR ?= $(CURDIR)/.cache/terraform/plugin-cache

.PHONY: help tools ci-generate-tools ci-lint-tools lint-tools lint lint-go lint-ui lint-shell lint-workflows lint-docker test-ci-classifier test-e2e-worker-harness dev-infra dev-infra-down dev-infra-clean generate generate-fast db-provision db-provision-test dev-bootstrap dev ui-review-reset ui-review-dev ui-review-screenshots test test-coverage test-deployment test-integration test-e2e test-e2e-ci test-e2e-workers terraform-fmt terraform-validate terraform-lint terraform-check verify verify-foundation reset-local fmt-check

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install pinned Go development tools into ./bin
	$(MAKE) ci-generate-tools
	GOBIN=$(BIN_DIR) go install github.com/air-verse/air@v1.67.1

ci-generate-tools: ## Install or validate the pinned CI generation tools in ./bin
	@mkdir -p $(BIN_DIR)
	@if $(TEMPL) version 2>/dev/null | grep -qx 'v0.3.1020' && $(SQLC) version 2>/dev/null | grep -qx 'v1.31.1'; then \
		echo 'validated cached generation tools'; \
	else \
		GOBIN=$(BIN_DIR) go install github.com/a-h/templ/cmd/templ@v0.3.1020; \
		GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1; \
		$(TEMPL) version | grep -qx 'v0.3.1020'; \
		$(SQLC) version | grep -qx 'v1.31.1'; \
	fi

lint-tools: ## Install pinned static-analysis tools into ./bin
	$(MAKE) ci-lint-tools

ci-lint-tools: ## Install or validate the pinned CI lint tools in ./bin
	@mkdir -p $(BIN_DIR)
	@if $(STATICCHECK) -version 2>/dev/null | grep -Fq '(v0.7.0)' && $(ACTIONLINT) -version 2>/dev/null | grep -qx 'v1.7.12'; then \
		echo 'validated cached lint tools'; \
	else \
		GOBIN=$(BIN_DIR) go install honnef.co/go/tools/cmd/staticcheck@v0.7.0; \
		GOBIN=$(BIN_DIR) go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; \
		$(STATICCHECK) -version | grep -Fq '(v0.7.0)'; \
		$(ACTIONLINT) -version | grep -qx 'v1.7.12'; \
	fi

lint: lint-tools ## Run source, workflow, shell, Dockerfile, and Terraform linters
	npm ci
	$(MAKE) lint-go lint-ui lint-shell lint-workflows lint-docker terraform-lint

lint-go: ## Run Go static analysis
	$(STATICCHECK) ./internal/... ./cmd/... ./ui/...

lint-ui: ## Lint JavaScript, CSS, templ syntax, and HTMX usage
	npm run lint

lint-shell: ## Lint tracked shell scripts in a pinned ShellCheck container
	docker run --rm -v "$(CURDIR):/src:ro" -w /src $(SHELLCHECK_IMAGE) --exclude=SC1090,SC2016 $$(git ls-files '*.sh')

lint-workflows: ## Lint GitHub Actions workflows
	$(ACTIONLINT)

lint-docker: ## Lint application and Caddy Dockerfiles
	# Alpine patch packages follow the pinned base image repository; distroless supplies the named nonroot user.
	docker run --rm -i $(HADOLINT_IMAGE) hadolint --ignore DL3018 --ignore DL3066 - < Dockerfile
	docker run --rm -i $(HADOLINT_IMAGE) hadolint --ignore DL3018 --ignore DL3066 - < deployment/caddy.Dockerfile

test-ci-classifier: ## Test conservative documentation-only CI routing
	./scripts/classify-ci-change_test.sh

test-e2e-worker-harness: ## Test worker-trial validation and evidence parsing
	./scripts/e2e-worker-trial_test.sh
	node --test scripts/summarize-playwright-json.test.mjs

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
	go test ./internal/... ./cmd/... ./ui/...

test-coverage: ## Run unit tests and write text/HTML reports with a regression floor
	./scripts/go-coverage.sh

test-deployment: ## Run production release orchestration tests
	sh deployment/mycfc-pull-release-timer_test.sh
	sh deployment/pull-release_test.sh
	sh deployment/release-status_test.sh
	sh deployment/publish-release-image_test.sh

test-integration: dev-infra db-provision-test ## Run integration tests against local services
	@set -a; source .env; set +a; TEST_DATABASE_URL="postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@localhost:5432/mycfc_test?sslmode=disable" go test -tags=integration ./internal/db/... ./internal/handlers/... ./internal/storage/...

test-e2e: dev-bootstrap ## Run browser and accessibility tests
	docker compose --profile e2e up --force-recreate --abort-on-container-exit --exit-code-from e2e e2e-app e2e

test-e2e-ci: ## Run the lean CI browser and accessibility gate
	./scripts/e2e-ci-bootstrap.sh
	docker compose -f compose.yaml -f compose.e2e-ci.yaml --profile e2e up --force-recreate --abort-on-container-exit --exit-code-from e2e e2e-app e2e

test-e2e-workers: ## Record 20 representative E2E runs (set E2E_WORKERS=2, 3, or 4)
	@test -n "$(E2E_WORKERS)" || { echo 'set E2E_WORKERS to the candidate worker count'; exit 2; }
	./scripts/e2e-worker-trial.sh "$(E2E_WORKERS)" "$(E2E_RUNS)"

terraform-fmt: ## Check Terraform formatting through the pinned container
	docker run --rm --user "$$(id -u):$$(id -g)" -v "$(CURDIR):/workspace" -w /workspace $(TERRAFORM_IMAGE) fmt -check -recursive infra

terraform-validate: ## Validate Terraform stacks through the pinned container without remote state
	@mkdir -p "$(TERRAFORM_PLUGIN_CACHE_DIR)"
	docker run --rm --user "$$(id -u):$$(id -g)" --entrypoint /bin/sh -v "$(CURDIR):/workspace" -v "$(TERRAFORM_PLUGIN_CACHE_DIR):/terraform-plugin-cache" -w /workspace -e TF_PLUGIN_CACHE_DIR=/terraform-plugin-cache $(TERRAFORM_IMAGE) -ec 'TF_DATA_DIR=/tmp/mycfc-bootstrap terraform -chdir=infra/bootstrap init -backend=false && TF_DATA_DIR=/tmp/mycfc-bootstrap terraform -chdir=infra/bootstrap validate && TF_DATA_DIR=/tmp/mycfc-hetzner terraform -chdir=infra/environments/hetzner init -backend=false && TF_DATA_DIR=/tmp/mycfc-hetzner terraform -chdir=infra/environments/hetzner validate && TF_DATA_DIR=/tmp/mycfc-production terraform -chdir=infra/environments/production init -backend=false && TF_DATA_DIR=/tmp/mycfc-production terraform -chdir=infra/environments/production validate'

terraform-lint: ## Run built-in TFLint rules for every Terraform root module
	docker run --rm -v "$(CURDIR):/workspace" -w /workspace $(TFLINT_IMAGE) --chdir=infra/bootstrap --format=compact
	docker run --rm -v "$(CURDIR):/workspace" -w /workspace $(TFLINT_IMAGE) --chdir=infra/environments/hetzner --format=compact
	docker run --rm -v "$(CURDIR):/workspace" -w /workspace $(TFLINT_IMAGE) --chdir=infra/environments/production --format=compact

terraform-check: terraform-fmt terraform-validate terraform-lint ## Run all containerized Terraform checks

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

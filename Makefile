SHELL := /usr/bin/env bash
.SHELLFLAGS := -Eeuo pipefail -c
.DEFAULT_GOAL := help

BIN_DIR := $(CURDIR)/bin
export PATH := $(BIN_DIR):$(PATH)

# Keep the release help text and recipe in sync with scripts/release.sh.
.PHONY: help tools ci-generate-tools ci-lint-tools lint-tools lint lint-go lint-ui lint-shell lint-workflows lint-docker test-ci-classifier test-e2e-worker-harness test-release-tooling test-release-upgrade release approval-packet dev-infra dev-infra-down dev-infra-clean generate generate-fast db-provision db-provision-test dev-bootstrap dev ui-review-reset ui-review-dev ui-review-screenshots test test-coverage test-deployment test-integration test-e2e test-e2e-ci test-e2e-workers terraform-fmt terraform-validate terraform-test terraform-lint terraform-check verify verify-foundation reset-local fmt-check

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install repository-local tools into ./bin
	@mkdir -p $(BIN_DIR)
	@# rest of makefile - PLACEHOLDER

# 08 — GitHub Actions CI, infrastructure GitOps and production deployment

## 1. Objective

Create reproducible workflows that verify all generated/code/infrastructure artefacts, authenticate to AWS only through OIDC, build one immutable image, run backward-compatible migrations as an ECS one-off task, deploy by digest, and verify/roll back failures.

All third-party actions MUST be pinned to full commit SHA with an adjacent comment naming the human-readable release. Dependabot is configured to update action SHAs, Go modules, npm and Docker dependencies.

## 2. Workflows

Create:

```text
.github/workflows/ci.yml
.github/workflows/infra.yml
.github/workflows/deploy.yml
.github/dependabot.yml
```

## 3. `ci.yml`

Triggers: pull requests and pushes to `main`. Cancel superseded runs per branch.

Permissions: `contents: read`; add no write permission unless a specific step needs it.

Jobs:

1. **generated-and-format**
   - Checkout.
   - Set up Go 1.26.5 and Node version pinned in `.node-version`.
   - `npm ci`.
   - `make generate`.
   - `gofmt`/`goimports` check.
   - Fail if Git diff is non-empty.

2. **go-test**
   - Start PostgreSQL and MinIO through Compose or service containers.
   - `make migrate-up`.
   - `go test -race -count=1 ./...`.
   - `go vet ./...`.
   - `govulncheck ./...`.

3. **frontend**
   - `npm ci`.
   - `npm run build`.
   - `npm audit --audit-level=high`; allow no high/critical unresolved production dependency.
   - Playwright/axe test suite using built application.

4. **container**
   - Build production Docker image with BuildKit and local cache.
   - Run image as non-root and smoke `/health/live`.
   - Generate SBOM.
   - Scan image; fail on fixable critical/high vulnerabilities, with reviewed expiry-dated allowlist only.

5. **summary** depends on all jobs and is the sole required branch-protection check.

## 4. `infra.yml`

Triggers:

- Pull requests touching `infra/**`: format, validate, lint, security scan and production plan using `github-infra-plan` OIDC role. The plan summary is added to GitHub job summary; do not upload a plan containing secrets from forks.
- Push to `main` touching `infra/**`: protected `production` environment, use `github-infra-apply`, repeat validation, create fresh plan, then apply exactly that saved plan.
- Manual dispatch supports plan-only; destructive apply is not a free-form flag.

Permissions: `id-token: write`, `contents: read`, and only the GitHub permissions required to report checks.

Use concurrency group `mycfc-production-infra` with `cancel-in-progress: false` for apply.

Bootstrap stack is not automatically applied by normal workflow; document one-time secure bootstrap. Production stack is fully GitOps after bootstrap.

## 5. `deploy.yml`

Trigger: successful `ci.yml` completion for `main`, plus manual dispatch of a specific main-branch commit. It uses protected GitHub environment `production`. Concurrency `mycfc-production-deploy`, never cancel in progress.

Permissions: `id-token: write`, `contents: read`, `attestations: write` when image attestation is enabled.

### Deployment sequence

1. Verify requested SHA is reachable from `main` and matches the CI-tested SHA.
2. Checkout exact SHA.
3. Configure AWS credentials by OIDC using `github-deploy` role.
4. Login to ECR.
5. Build once with production Dockerfile; tag `git-<40sha>`; push.
6. Resolve and record immutable ECR digest. Subsequent steps use `<repo>@sha256:<digest>` only.
7. Generate SBOM and provenance/attestation tied to digest.
8. Fetch current ECS task definition JSON; create a new revision changing only image digest, app version/Git SHA and permitted deployment metadata. Strip read-only AWS fields before registration.
9. Run migration as an ECS one-off Fargate task using the new task definition revision with command override `[/app/mycfc, migrate, up]`, private app subnets and migration SG/role configuration.
10. Wait `tasks-stopped`. Query `stopCode`, `stoppedReason`, container `exitCode` and reason. Fail unless the migration container exit code is exactly 0. Print bounded migration logs on failure without secret values.
11. Update ECS service to the new task-definition revision with force-new-deployment.
12. Wait for services stable with an explicit maximum polling duration. Then confirm primary deployment is completed and target health is healthy.
13. Smoke test `https://<domain>/health/ready`, login GET, and static asset response. Retry boundedly to tolerate DNS/ALB propagation.
14. Write deployment summary containing SHA, image digest, task-definition revision and migration task ARN—not secrets.

If service deployment fails, ECS circuit breaker performs rollback. Workflow reports the rolled-back status and fails. Do not automatically run down migrations.

## 6. Migration compatibility policy

Every normal migration MUST be backward-compatible with the currently deployed release:

- Add nullable columns or columns with safe defaults before code depends on them.
- Add tables/indexes concurrently where PostgreSQL requires low-lock rollout; Goose files requiring `NO TRANSACTION` must declare it.
- Do not rename/drop columns, tighten constraints over existing data, remove enum values or make columns non-null in the same release that stops using the old shape.
- Destructive contract migrations require a later dedicated release after telemetry confirms old code/data is absent and require manual production approval.

CI includes a migration compatibility review checklist. The agent must document non-trivial migrations in `docs/migrations/<id>.md`.

## 7. Secrets and variables

No AWS access keys in GitHub secrets.

GitHub environment variables may contain non-secret identifiers: region, Terraform backend bucket, domain, ECS cluster/service, ECR repository. Prefer deriving them from Terraform remote-state outputs using scoped read permissions to avoid duplication.

Never print task-definition `secrets`, Terraform sensitive outputs, database endpoints with credentials, CSRF keys or full environment dumps.

## 8. Dockerfile contract

Multi-stage build:

1. Node stage: `npm ci` and production asset build.
2. Go builder stage pinned to Go 1.26.5; copy module files first; download; generate templ/sqlc or verify committed generation; build static `CGO_ENABLED=0` binary with version/SHA via ldflags.
3. Final distroless/static non-root image. Copy CA certificates, timezone data if required, binary and no source/build tools. User non-root; read-only filesystem compatible; entrypoint `/app/mycfc serve`.

The same binary supports `serve` and `migrate up`; migration command exits non-zero on any Goose error.

## 9. Supply-chain and workflow safety

- Pull requests from forks never receive AWS OIDC deployment privileges or environment secrets.
- Avoid `pull_request_target` for executing repository code.
- Quote all shell variables; use `set -Eeuo pipefail`.
- Do not execute untrusted branch content in a privileged deploy job.
- Pin base images by digest with readable version comments; automated updates open PRs.
- Build context excludes `.git`, local env files, Terraform state and test artefacts.

## 10. Acceptance criteria

- PR cannot deploy or apply production infrastructure.
- Workflow OIDC subject matches protected production environment trust exactly.
- A migration task non-zero exit prevents service update.
- Deployment uses digest and can prove which commit produced it.
- An unhealthy image triggers ECS rollback and the workflow fails.
- Re-running deployment for the same SHA is safe: migration is already applied, new task revision may be registered, resulting service image digest is unchanged.
- No workflow has `permissions: write-all`.
- All action references are 40-character SHAs.
- CI catches generated-code drift, schema/query compile errors, race failures, a11y serious/critical issues and high/critical image vulnerabilities.

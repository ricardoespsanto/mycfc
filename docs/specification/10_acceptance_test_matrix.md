# 10 — Final acceptance test matrix and definition of completion

## 1. Purpose

This is the release gate. The coding agent MUST implement and run every mandatory check. A visually plausible scaffold is not completion.

## 2. Mandatory static commands

From a clean checkout after dependencies are installed:

```bash
make dev-infra
make migrate-up
make generate
git diff --exit-code
gofmt -l . | test -z "$(cat)"
go vet ./...
govulncheck ./...
go test -race -count=1 ./...
npm ci
npm run build
npm audit --audit-level=high
terraform -chdir=infra/bootstrap fmt -check
terraform -chdir=infra/bootstrap validate
terraform -chdir=infra/environments/production fmt -check
terraform -chdir=infra/environments/production validate
tflint --chdir=infra/environments/production
```

`make verify` MUST run the applicable superset and fail on the first failing phase with a clear label.

## 3. Database scenarios

| ID | Scenario | Expected result |
|---|---|---|
| DB-01 | Fresh PostgreSQL 16, Goose up | All migrations apply |
| DB-02 | Goose status after up | No pending migrations |
| DB-03 | Local down-all then up | Reversible in local/test |
| DB-04 | Duplicate email differing only case | Rejected |
| DB-05 | Invalid role/squad/dependent combinations | Rejected by DB and app |
| DB-06 | Delete user with consent and repair | Consent cascades; repair reporter becomes null |
| DB-07 | Concurrent repair same idempotency UUID | Exactly one repair row |
| DB-08 | sqlc regeneration | No diff |

## 4. Authentication and authorisation scenarios

| ID | Scenario | Expected result |
|---|---|---|
| AUTH-01 | Adult registration valid for each public role | User + exact consent rows committed, logged in |
| AUTH-02 | One consent insert fails | Entire transaction rolled back |
| AUTH-03 | Under-18 adult registration | 422 |
| AUTH-04 | Password 11 bytes or >72 bytes | 422 |
| AUTH-05 | Unknown/wrong/inactive/dependent login | Identical user-visible error |
| AUTH-06 | Successful login | Session token renewed |
| AUTH-07 | GET logout | 405 |
| AUTH-08 | POST logout | Session invalid and redirect login |
| AUTH-09 | Role accesses another dashboard | 403 |
| AUTH-10 | Unauthenticated normal/HTMX protected request | Redirect / HX-Redirect semantics |
| AUTH-11 | Open redirect payload in next | Ignored |
| AUTH-12 | Guardian creates dependent | Guardian derived from session/DB; consent audit correct |
| AUTH-13 | Non-Guardian or 11th dependent | Forbidden / 422 |

## 5. Repair and storage scenarios

| ID | Scenario | Expected result |
|---|---|---|
| REP-01 | Valid report without photo | 1 pending row |
| REP-02 | Valid JPEG/PNG/WebP | Private object + exact DB metadata |
| REP-03 | SVG, GIF, MIME mismatch, zero-byte, oversized dimensions | 422 before object upload |
| REP-04 | Request > global limit | 413 |
| REP-05 | DB fails after S3 upload | Delete compensation attempted; no row |
| REP-06 | Same request replay | Same semantic success, no duplicate retained object |
| REP-07 | Cross-user idempotency collision | 409 |
| REP-08 | Anonymous S3 GET | Denied |
| REP-09 | Admin pre-signed URL | Works for configured lifetime, then fails |

## 6. UI, localisation and accessibility scenarios

| ID | Scenario | Expected result |
|---|---|---|
| UI-01 | Every page with JS disabled | Core flows work |
| UI-02 | HTMX validation | 422 swaps form; focus error summary |
| UI-03 | HTMX success | Status announced; button restored |
| UI-04 | All four role dashboards empty/populated | Correct data and empty states |
| UI-05 | 320px viewport and 200% zoom | No page-level horizontal overflow; operable |
| UI-06 | Keyboard-only end-to-end | All controls reachable/usable |
| UI-07 | Axe scan all required states | No serious/critical violations |
| UI-08 | Rendered language scan | No banned pt-BR terms; html lang pt-PT |
| UI-09 | Calendar API failure | Accessible warning + fallback links |
| UI-10 | Inline assets/CDN scan | No inline script/style or runtime CDN |

## 7. HTTP and security scenarios

| ID | Scenario | Expected result |
|---|---|---|
| SEC-01 | Missing/invalid CSRF on every POST | 403, handler not called |
| SEC-02 | Security headers production | Exact required policy present |
| SEC-03 | Untrusted X-Forwarded-For/Proto | Ignored |
| SEC-04 | Trusted ALB forwarding | Correct client IP/scheme used |
| SEC-05 | Panic in handler | 500 generic page, request ID, process remains alive |
| SEC-06 | Malformed/oversized forms | Bounded 4xx; no panic/memory blow-up |
| SEC-07 | Logs inspection | No secrets, passwords, cookies, raw DB URL or CSRF tokens |
| SEC-08 | Cookie inspection | Secure/HttpOnly/SameSite/path/lifetime correct |
| SEC-09 | Unknown route and wrong method | pt-PT 404; 405 with Allow |

## 8. Container and runtime scenarios

| ID | Scenario | Expected result |
|---|---|---|
| RUN-01 | Final image user | Non-root |
| RUN-02 | Read-only root filesystem | App runs and uploads still work |
| RUN-03 | SIGTERM during request | Graceful shutdown within timeout |
| RUN-04 | DB unavailable at startup | Process exits non-zero |
| RUN-05 | DB lost after startup | readiness 503, liveness 200, recovery without restart |
| RUN-06 | Image contents | No source, node, compiler, package manager or shell |
| RUN-07 | Vulnerability scan | No unwaived fixable high/critical findings |

## 9. Infrastructure scenarios

| ID | Scenario | Expected result |
|---|---|---|
| INF-01 | Terraform plan from clean state | Complete graph and no manual console resources except declared prerequisites |
| INF-02 | Second apply | No drift |
| INF-03 | Network reachability | Internet→ALB only; task/RDS direct denied |
| INF-04 | Private task AWS access | ECR/logs/secrets/S3 work without NAT/public IP |
| INF-05 | S3 policy | Public access/unencrypted transport denied |
| INF-06 | OIDC wrong repo/subject | AssumeRole denied |
| INF-07 | IAM pass-role | Only named ECS roles allowed |
| INF-08 | RDS deletion attempt | Blocked by deletion protection |
| INF-09 | ALB HTTP | Redirects HTTPS; valid ACM chain |
| INF-10 | WAF rate test in non-prod clone | Rate rule blocks without affecting health checks |

## 10. Deployment scenarios

| ID | Scenario | Expected result |
|---|---|---|
| DEP-01 | Successful main deployment | Digest deployed, migration 0, smoke green |
| DEP-02 | Migration exits non-zero | Service not updated |
| DEP-03 | App never becomes healthy | Circuit breaker rollback; workflow fails |
| DEP-04 | Re-run same SHA | Safe and same digest |
| DEP-05 | PR/fork workflow | No production OIDC or secrets |
| DEP-06 | Action reference scan | Every `uses:` third-party ref is full SHA |
| DEP-07 | Concurrent deployments | Serialized; none cancelled mid-migration |
| DEP-08 | Terraform after app deploy | Does not revert task revision |

## 11. Evidence required

The final implementation PR/agent report MUST include:

- Command summary with pass/fail counts.
- Integration and E2E test report paths.
- Axe report.
- Container SBOM and vulnerability summary.
- Terraform plan summary with sensitive values redacted.
- Deployed image digest for production deployment runs.
- List of deliberately deferred product features, which may include password reset/email delivery, consent revocation UI, user profile editing and admin content authoring. Deferred items must not be represented by active dead-end controls.

## 12. Hard completion blockers

The implementation is not complete if any applies:

- A route renders placeholder data where a DB-backed section is required.
- Any success handler is a stub.
- Any production secret has a fallback/default.
- App tasks or RDS are public.
- Images are public or public URLs are stored.
- Migrations run automatically in every web task.
- App correctness depends on in-memory session/idempotency/rate-limit state.
- A role check trusts only browser input or session role without DB user validation.
- Tests are skipped, marked flaky without issue/expiry, or weakened to fit implementation.
- Documentation and Make targets disagree.
- Unresolved architecture decisions remain in TODO comments.

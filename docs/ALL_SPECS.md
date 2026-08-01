# MyCFC — Complete production implementation specification

This combined file is generated from the canonical ordered files. The split files remain easier to maintain. Architecture diagrams and their editable Graphviz sources remain separate binary/text artefacts referenced from this document.


---

<!-- BEGIN README.md -->

# MyCFC production implementation specification

This directory replaces the original short prompts with one canonical, internally consistent implementation contract intended for autonomous implementation by an LLM coding agent.

## Execution order

The agent MUST process the documents in numeric order and treat earlier global decisions as binding:

1. `00_system_context.md`
2. `01_data_models.md`
3. `02_routing_and_middleware.md`
4. `03_htmx_dashboards.md`
5. `04_equipment_workflow.md`
6. `05_auth_and_consent.md`
7. `06_frontend_a11y_pt_PT.md`
8. `07_aws_deployment_gitops.md`
9. `08_github_actions_pipeline.md`
10. `09_local_dev_and_minio.md`
11. `10_acceptance_test_matrix.md`

## Architecture diagrams

The architecture is deliberately split by concern:

- `architecture_runtime.svg` is the normative visual for **running components, trust boundaries, request paths and data flows**.
- `architecture_delivery_pipeline.svg` is the normative visual for **source verification, Terraform reconciliation, database migration and application deployment**.
- `architecture_runtime.dot` and `architecture_delivery_pipeline.dot` are the editable canonical sources. Regenerate the SVGs with Graphviz rather than editing generated SVG markup by hand.
- `ARCHITECTURE.md` presents both diagrams with their scope and regeneration commands.

The runtime diagram MUST NOT accumulate CI/CD components. The delivery diagram MUST collapse the running application into a single referenced runtime boundary instead of duplicating the full topology. The Markdown documents remain authoritative when a visual detail is unclear.

## Meaning of “unattended implementation ready”

The implementation agent MUST NOT ask the operator to choose architecture, packages, route behaviour, database constraints, error semantics, deployment order, or test strategy. Those decisions are fixed here.

The operator still has to supply external account-specific values that cannot safely be invented: AWS account/region, Route 53 hosted-zone ID, GitHub organisation and repository, production domain, public Google Calendar IDs, browser API key, gallery URL, and current legal-document versions/hashes. Terraform variables and environment validation MUST fail clearly when one is absent.

## Deliberate architecture correction

The production target is **Amazon ECS on Fargate behind an Application Load Balancer**, not AWS App Runner. App Runner stopped accepting new customers on 31 March 2026, so it is not a valid default for a deployable new production environment.

## Completion rule

The repository is complete only when every mandatory command and scenario in `10_acceptance_test_matrix.md` passes and there are no unresolved `TODO`, `FIXME`, dummy secrets, wildcard IAM resources that are not explicitly permitted, or placeholder runtime values.

<!-- END README.md -->


---

<!-- BEGIN 00_system_context.md -->

# 00 — System context and autonomous implementation contract

## 1. Objective

Build and deploy **MyCFC**, a production-grade monolithic web application for Clube Fluvial de Coimbra. It provides role-specific dashboards, authentication, guardian/dependent registration, consent audit records, equipment repair reporting with private object storage, maintenance visibility, public-calendar integration, and administrator fleet oversight.

This file has highest precedence. Later files may add detail but MUST NOT contradict it.

## 2. Fixed technical decisions

| Concern | Required decision |
|---|---|
| Go toolchain | Go `1.26.5`; `go.mod` module `github.com/cfcoimbra/mycfc` and `go 1.26.0` |
| HTTP | Standard-library `net/http` router and patterns; no third-party router |
| Rendering | Server-side rendering with `github.com/a-h/templ` |
| Interaction | HTMX with response-targets extension; progressive enhancement is mandatory |
| Styling | Pico CSS, locally bundled; no runtime CDN dependency |
| Browser assets | npm lockfile + esbuild; locally bundled and embedded into the Go binary |
| Database | PostgreSQL 16, `timestamptz`, UUID primary keys |
| DB driver | Native `github.com/jackc/pgx/v5/pgxpool`; do not use `database/sql` |
| Data access | `sqlc` using `sql_package: pgx/v5`; handwritten SQL outside `schema.sql`/queries is forbidden |
| Schema provisioning | `internal/db/schema.sql` is a reset-only baseline applied with `psql`; it is not an in-place production migration mechanism |
| Sessions | `github.com/alexedwards/scs/v2` with PostgreSQL `pgxstore` |
| CSRF | `github.com/gorilla/csrf`; all state-changing browser requests protected |
| Passwords | `golang.org/x/crypto/bcrypt`, cost 12 |
| Configuration | `github.com/caarlos0/env/v11`; startup validation must aggregate all errors |
| Logging | `log/slog` JSON in production, text in local development |
| Storage | Private S3 bucket via AWS SDK for Go v2 `s3.Client`; store object keys, never public URLs |
| Local storage | MinIO with path-style addressing |
| Production compute | ECS Fargate service in private subnets behind a public ALB and AWS WAF |
| Infrastructure | Terraform with remote S3 state and S3 lockfile; no deprecated DynamoDB state lock |
| CI/CD | GitHub Actions with OIDC; no static AWS credentials |
| Primary locale | European Portuguese `pt-PT` only |
| Application timezone | `Europe/Lisbon`; timestamps stored as `timestamptz` and rendered in this location |

Dependency versions MUST be recorded in `go.mod`, `go.sum`, `package-lock.json`, `.terraform.lock.hcl`, and pinned GitHub Action commit SHAs. Do not use floating `latest` versions in production files.

## 3. Architecture invariants

`architecture_runtime.svg` and `architecture_delivery_pipeline.svg` are complementary normative views. The first defines the running topology and request/data flows; the second defines how verified source and infrastructure changes reach that topology. Their editable sources are the corresponding `.dot` files.

1. The application is a single stateless Go process. Session state and business data live in PostgreSQL.
2. Two or more ECS tasks may process requests concurrently. No correctness property may depend on process-local memory.
3. All application tasks and migration tasks run in private subnets without public IP addresses.
4. The ALB is the only public application ingress. RDS and S3 are never public.
5. Browser calendar integration is allowed only for explicitly public Google Calendars. The browser API key is not a secret but MUST be restricted to the production and local origins in Google Cloud.
6. S3 repair images are private. Pages needing an image receive a short-lived pre-signed GET URL generated server-side.
7. Database migrations MUST use expand/contract compatibility. A deployment may run new code and old code simultaneously during a rolling update.
8. The application MUST remain usable for core tasks without JavaScript: login, registration, dashboard navigation, dependent creation, and repair submission.
9. All user-facing text, validation messages, date formatting, and accessibility labels are pt-PT.
10. Production startup MUST fail closed when required configuration is invalid. It must not silently use insecure defaults.

## 4. Required external production inputs

Terraform variables MUST define and validate the following. No default may be supplied for account-specific values:

- `aws_region` — default may be `eu-west-1`, but it must remain overrideable.
- `project_name` — default `mycfc`.
- `environment` — exactly `production` for the production stack.
- `domain_name` — expected `mycfc.pt` unless the operator overrides it.
- `route53_zone_id` — existing public hosted zone.
- `github_org` and `github_repo`.
- `github_environment` — default `production`.
- `calendar_competition_id`, `calendar_training_id`, `calendar_social_id`, and `calendar_cleanups_id` — public calendar IDs.
- `google_calendar_api_key` — browser key supplied as a secret Terraform variable and delivered to the container as runtime configuration.
- `gallery_url` — HTTPS URL.
- Current versions and lowercase SHA-256 digests for the three legal texts: `Termos_Gerais`, `Uso_Imagem`, and `Responsabilidade_Menor`.
- Optional SNS alarm recipient email. If absent, alarms still exist but no email subscription is created.

The repository MUST include `terraform.tfvars.example` and `.env.example` with non-secret explanatory values only.

## 5. Repository layout

The implementation MUST use this layout. Generated files may add children but not replace these boundaries.

```text
.
├── cmd/
│   ├── server/main.go
│   └── admin/main.go                 # create-admin and set-password CLI
├── internal/
│   ├── app/                          # dependency wiring and server lifecycle
│   ├── auth/                         # auth/role middleware and session helpers
│   ├── config/                       # environment parsing and validation
│   ├── db/
│   │   ├── migrations/
│   │   ├── queries/
│   │   └── generated/
│   ├── handlers/
│   ├── httpx/                        # response helpers, request IDs, errors
│   ├── locale/                       # pt-PT messages and formatters
│   ├── storage/                      # S3 interface and implementation
│   └── validation/
├── ui/
│   ├── components/
│   ├── pages/
│   ├── static/src/
│   └── static/dist/                  # generated, embedded assets
├── tests/
│   ├── integration/
│   └── e2e/
├── infra/
│   ├── bootstrap/
│   └── environments/production/
├── .github/workflows/
├── compose.yaml
├── Dockerfile
├── Makefile
├── .air.toml
├── package.json
├── package-lock.json
├── sqlc.yaml
└── go.mod
```

## 6. Error and response contract

- Every request gets a cryptographically random request ID. Accept an inbound `X-Request-ID` only when it matches `^[A-Za-z0-9._-]{8,128}$`; otherwise replace it.
- Every response includes `X-Request-ID`.
- Internal errors are logged with request ID and wrapped causes; users receive a generic pt-PT message without stack traces, SQL, object keys, or secrets.
- HTML validation failures return `422 Unprocessable Entity` and re-render the form with field errors.
- Authentication failures return the same generic message regardless of whether the account exists.
- Authorisation failures return `403`; unauthenticated protected requests redirect to `/login?next=<safe-local-path>` for normal navigation and return `401` for HTMX requests with `HX-Redirect: /login`.
- Method mismatches return `405` with `Allow`.
- Unknown routes return a rendered pt-PT `404` page.

## 7. Security baseline

- Cookies: `HttpOnly`, `Secure` in production, `SameSite=Lax`, path `/`, 12-hour absolute lifetime, 30-minute idle renewal.
- Session token is renewed after login and privilege-relevant changes.
- CSRF authentication key is exactly 32 decoded bytes and loaded from a secure runtime secret.
- Set CSP, HSTS, `X-Content-Type-Options`, `Referrer-Policy`, frame restrictions, and a restrictive `Permissions-Policy` as specified in file 02.
- Do not log passwords, hashes, cookies, CSRF tokens, consent document contents, database URLs, AWS credentials, or full uploaded filenames.
- SQL parameters are always bound through generated sqlc methods.
- Uploaded bytes are treated as hostile and never rendered inline as HTML/SVG.
- AWS IAM policies use resource-level permissions wherever AWS supports them.

## 8. Implementation protocol for the coding agent

1. Read all numbered files before writing code.
2. Create a checklist mapped to every acceptance criterion in file 10.
3. Implement in numeric order, keeping the repository buildable after each file.
4. Generate templ and sqlc output; commit generated code.
5. Add tests at the same time as each behaviour, not afterward.
6. Run `make verify` after every logical phase and at completion.
7. Do not weaken tests or acceptance criteria to obtain a green build.
8. Do not leave stubs that return successful responses.
9. Where a required external production value is absent, implement the variable/configuration and fail validation; do not invent a production value.
10. When third-party API behaviour differs from this specification, use the official current API, preserve the intended security property, and document the exact adaptation in `implementation-status.md` and the relevant GitHub Project issue.

## 9. Global definition of done

- All commands and scenarios in file 10 pass.
- `git grep -nE 'TODO|FIXME|CHANGEME|example\.com|<YOUR_|dummy|insecure'` returns no production-code findings except explicitly allowed examples in `*.example` files and documentation.
- Fresh clone + documented local bootstrap works without manual database or bucket operations.
- Terraform formatting, validation and static checks pass.
- GitHub workflows contain no long-lived cloud credentials and all third-party actions are pinned by full commit SHA.
- Production image runs as a non-root user, has no shell/package manager in the final stage, and exposes only port 8080.

<!-- END 00_system_context.md -->


---

<!-- BEGIN 01_data_models.md -->

# 01 — PostgreSQL schema, sqlc and transactional contracts

## 1. Objective

Create a complete PostgreSQL 16 schema and generated data-access layer that can support every route and dashboard without ad-hoc SQL or invented fields.

## 2. Files to create

```text
internal/db/schema.sql
internal/db/queries/users.sql
internal/db/queries/consents.sql
internal/db/queries/equipment.sql
internal/db/queries/repairs.sql
internal/db/queries/dashboards.sql
internal/db/queries/maintenance.sql
internal/db/queries/whatsapp.sql
sqlc.yaml
```

`internal/db/schema.sql` is the complete ordered baseline for newly created local and test databases. It has no down path and must be applied with `psql -v ON_ERROR_STOP=1` only to a reset database.

## 3. Extensions and enum types

Migration `00001` MUST create `citext` and `pgcrypto` with `IF NOT EXISTS`, then create exactly these enum types:

```sql
role                = 'Admin', 'Competitor', 'Leisure', 'Guardian'
squad_category      = 'Iniciante', 'Polo_Senior', 'Master_A', 'Lazer', 'None'
repair_status       = 'Pendente', 'Em_Analise', 'Resolvido'
consent_type        = 'Termos_Gerais', 'Uso_Imagem', 'Responsabilidade_Menor'
equipment_type      = 'Boat', 'Paddle', 'Vehicle'
equipment_status    = 'Operational', 'Maintenance', 'Retired'
maintenance_status  = 'Scheduled', 'In_Progress', 'Completed', 'Cancelled'
metric_type         = 'Distance_Metres', 'Duration_Seconds', 'Sessions', 'Custom'
```

UUID defaults use `gen_random_uuid()`. All timestamp columns use `timestamptz` with `NOT NULL DEFAULT now()` unless explicitly nullable.

## 4. Core tables

### 4.1 `users`

| Column | Type | Rules |
|---|---|---|
| `id` | uuid | PK, default generated |
| `name` | varchar(120) | not null; trimmed length 2–120 |
| `email` | citext | nullable; unique when present |
| `password_hash` | text | nullable |
| `role` | role | not null |
| `squad_category` | squad_category | not null, default `None` |
| `guardian_id` | uuid | nullable self-FK, `ON DELETE RESTRICT` |
| `is_dependent` | boolean | not null, default false |
| `date_of_birth` | date | not null |
| `is_active` | boolean | not null, default true |
| `created_at` | timestamptz | not null |
| `updated_at` | timestamptz | not null |

Required constraints:

- Dependents: `is_dependent = true`, `guardian_id IS NOT NULL`, `email IS NULL`, `password_hash IS NULL`, and role is `Competitor` or `Leisure`.
- Adults: `is_dependent = false`, `guardian_id IS NULL`, `email IS NOT NULL`, and `password_hash IS NOT NULL`.
- `guardian_id <> id`.
- `Admin` and `Guardian` must have `squad_category = 'None'`.
- `Leisure` must have `squad_category = 'Lazer'`.
- `Competitor` must have `squad_category IN ('Iniciante','Polo_Senior','Master_A')`.
- Email unique index is `UNIQUE (email)` and relies on `citext` case-insensitivity.
- Index `guardian_id` and `(role, is_active)`.

Age eligibility is validated in application code using the Europe/Lisbon calendar date; do not encode a changing-age expression as a database check.

### 4.2 `equipment`

Columns: `id`, `asset_tag varchar(40) UNIQUE NOT NULL`, `name varchar(120) NOT NULL`, `type equipment_type NOT NULL`, `status equipment_status NOT NULL DEFAULT 'Operational'`, `notes text NOT NULL DEFAULT ''`, `created_at`, `updated_at`.

Checks: asset-tag trimmed length 2–40; name trimmed length 2–120; notes length <= 4000. Index `(status, type)`.

### 4.3 `repair_requests`

Columns:

- `id uuid` PK.
- `idempotency_key uuid NOT NULL UNIQUE`.
- `equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT`.
- `reported_by_id uuid NULL REFERENCES users(id) ON DELETE SET NULL`.
- `issue_description varchar(2000) NOT NULL`, trimmed length 10–2000.
- `status repair_status NOT NULL DEFAULT 'Pendente'`.
- `image_object_key varchar(512) NULL`.
- `image_content_type varchar(100) NULL`.
- `image_size_bytes bigint NULL` with range 1–10485760.
- `date_reported timestamptz NOT NULL DEFAULT now()`.
- `updated_at timestamptz NOT NULL DEFAULT now()`.
- `resolved_at timestamptz NULL`.

Constraints:

- All three image metadata fields are either all null or all non-null.
- `resolved_at` is non-null only when status is `Resolvido`; when status is `Resolvido`, it must be non-null.
- Index `(status, date_reported DESC)`, `equipment_id`, and `reported_by_id`.

### 4.4 `consent_forms`

This is an append-only audit table. Application code MUST never update a row.

Columns:

- `id uuid` PK.
- `user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE` — person the consent concerns.
- `granted_by_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL` — adult who accepted; self for adult registration, guardian for dependent.
- `consent_type consent_type NOT NULL`.
- `document_version varchar(40) NOT NULL`.
- `document_sha256 char(64) NOT NULL` constrained to lowercase hex.
- `is_accepted boolean NOT NULL` constrained to true in this release.
- `date_signed timestamptz NOT NULL DEFAULT now()`.
- `ip_address inet NULL`.
- `user_agent varchar(512) NOT NULL DEFAULT ''`.

Unique constraint `(user_id, consent_type, document_version)`. Index `(user_id, consent_type, date_signed DESC)`.

Guardian consent must set `granted_by_user_id` to a different user than `user_id`. Adult self-consent must set it equal to `user_id`; enforce this in application transaction tests because the table cannot infer dependency state without a trigger. Do not add a trigger.

### 4.5 `whatsapp_groups`

Columns: `id`, `name varchar(120)`, `discipline varchar(80)`, `target_role role`, `squad_category squad_category NULL`, `url text`, `is_active boolean default true`, `created_at`, `updated_at`.

Checks: URL starts with `https://chat.whatsapp.com/`; unique `(name, target_role, squad_category)` with nulls treated as not distinct. Index target role and active state.

## 5. Dashboard and maintenance tables

### 5.1 `training_logs`

`id`, `user_id` FK users cascade, `occurred_at`, `duration_seconds integer` range 60–86400, `distance_metres integer` range 0–200000, `notes varchar(2000) default ''`, `created_at`. Index `(user_id, occurred_at DESC)`.

### 5.2 `performance_metrics`

`id`, `user_id` FK cascade, `metric_type`, `label_pt varchar(100)`, `value numeric(12,2)`, `unit_pt varchar(30)`, `measured_at`, `created_at`. Index `(user_id, measured_at DESC)`.

### 5.3 `news_items`

`id`, `title_pt varchar(180)`, `summary_pt varchar(1000)`, `url text NULL`, `published_at`, `is_published boolean default false`, `created_at`, `updated_at`. URL must be null or HTTPS. Index `(is_published, published_at DESC)`.

### 5.4 `maintenance_tasks`

`id`, `equipment_id` FK restrict, `scheduled_for timestamptz`, `description varchar(2000)`, `status maintenance_status default 'Scheduled'`, `created_by_id` FK users set null, `completed_at nullable`, `created_at`, `updated_at`.

Constraints: completed status and timestamp agree. Index `(status, scheduled_for)` and `equipment_id`.

### 5.5 `sessions`

Use the exact schema required by the selected SCS pgx store:

```sql
CREATE TABLE sessions (
    token text PRIMARY KEY,
    data bytea NOT NULL,
    expiry timestamptz NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
```

## 6. Updated-at behaviour

Do not use database triggers. Every sqlc update query MUST set `updated_at = now()` explicitly. This keeps mutation behaviour visible in generated queries and tests.

## 7. Required sqlc query surface

Use these exact query names and cardinalities. Additional queries require a documented acceptance-test need.

### Users

- `CreateAdultUser :one`
- `CreateDependentUser :one`
- `GetUserByID :one`
- `GetActiveUserByEmail :one`
- `ListDependentsByGuardian :many`
- `CountDependentsByGuardian :one`
- `SetUserPasswordHash :exec`
- `DeactivateUser :exec`

### Consents

- `CreateConsentForm :one`
- `ListConsentFormsForUser :many`
- `HasConsentVersion :one`

### Equipment and repairs

- `ListOperationalEquipment :many`
- `ListEquipmentForAdmin :many`
- `GetEquipmentByID :one`
- `CreateRepairRequest :one`
- `GetRepairByIdempotencyKey :one`
- `GetRepairRequestByID :one`
- `ListPendingRepairRequests :many`
- `UpdateRepairStatus :one`

### Dashboards

- `ListRecentTrainingLogs :many`
- `ListRecentPerformanceMetrics :many`
- `ListPublishedNews :many`
- `ListWhatsAppGroupsForRole :many`
- `CountEquipmentByStatus :many`
- `ListUpcomingMaintenance :many`

### Maintenance

- `CreateMaintenanceTask :one`
- `CompleteMaintenanceTask :one`
- `CancelMaintenanceTask :one`

All list queries MUST have deterministic ordering and explicit `LIMIT` parameters. Do not use `SELECT *`.

## 8. `sqlc.yaml`

- Version 2 configuration.
- Engine `postgresql`.
- Schema points to `internal/db/schema.sql`; queries point to `internal/db/queries`.
- Go output `internal/db/generated`.
- Package name `dbgen`.
- `sql_package: pgx/v5`.
- `emit_interface: true`, `emit_json_tags: true`, `emit_empty_slices: true`, `emit_pointers_for_null_types: true`.
- Override PostgreSQL UUID to `github.com/google/uuid.UUID`; nullable UUID uses pointer.

## 9. Transaction contracts

Create `internal/db/tx.go` with a transaction helper accepting `pgx.TxOptions`, always rolling back on callback failure or panic, and committing only on nil error. The following operations MUST be single transactions:

- Adult registration: user + two consent rows.
- Dependent registration: dependent user + responsibility consent row.
- Admin status resolution: repair status + related maintenance changes when applicable.

No transaction may include S3 network I/O.

## 10. Tests and acceptance criteria

- Migration up/down/up succeeds against a fresh PostgreSQL 16 container.
- Every constraint above has at least one integration test that demonstrates rejection.
- Concurrent inserts with the same repair idempotency key produce one row.
- Case-variant duplicate emails are rejected.
- Deleting a user cascades their consent forms and sets `reported_by_id` to null on repair requests.
- sqlc generation is deterministic and leaves no Git diff.
- `go test ./internal/db/...` passes with race detection where applicable.

<!-- END 01_data_models.md -->


---

<!-- BEGIN 02_routing_and_middleware.md -->

# 02 — Configuration, application wiring, routing and middleware

## 1. Objective

Implement deterministic startup, secure middleware ordering, exact routes, resilient connection management, health checks, graceful shutdown, and uniform response semantics.

## 2. Configuration contract

Create `internal/config/config.go`. Parse with `caarlos0/env/v11`, then run explicit validation that returns one combined error listing every invalid field. Secrets must be redacted from errors and `%+v` output.

### Required fields

| Environment variable | Type / validation |
|---|---|
| `APP_ENV` | `local`, `test`, or `production` |
| `APP_VERSION` | non-empty release identifier |
| `GIT_SHA` | 40 lowercase hex in production |
| `PORT` | integer 1–65535, default 8080 |
| `BASE_URL` | absolute HTTPS URL in production; local HTTP permitted |
| `DATABASE_URL` | PostgreSQL URL; required; never logged |
| `CSRF_AUTH_KEY_B64` | base64 decoding to exactly 32 bytes |
| `AWS_REGION` | non-empty |
| `S3_BUCKET_NAME` | DNS-compatible bucket name |
| `S3_ENDPOINT` | empty in production; absolute URL locally |
| `S3_FORCE_PATH_STYLE` | true locally, false in production |
| `GOOGLE_CALENDAR_API_KEY` | non-empty browser key |
| `CALENDAR_COMPETITION_ID` | non-empty public calendar ID |
| `CALENDAR_TRAINING_ID` | non-empty public calendar ID |
| `CALENDAR_SOCIAL_ID` | non-empty public calendar ID |
| `CALENDAR_CLEANUPS_ID` | non-empty public calendar ID |
| `GALLERY_URL` | absolute HTTPS URL, local HTTP allowed in local env |
| `CONSENT_TERMS_VERSION` / `_SHA256` | version non-empty; lowercase 64-char hex |
| `CONSENT_IMAGE_VERSION` / `_SHA256` | same |
| `CONSENT_MINOR_VERSION` / `_SHA256` | same |

### Operational defaults

- `LOG_LEVEL=INFO`.
- `DB_MAX_CONNS=8`, `DB_MIN_CONNS=1`.
- `DB_MAX_CONN_LIFETIME=30m`, `DB_MAX_CONN_IDLE_TIME=5m`, `DB_HEALTH_CHECK_PERIOD=30s`.
- `SESSION_LIFETIME=12h`, `SESSION_IDLE_TIMEOUT=30m`.
- `MAX_REQUEST_BYTES=12582912`, `MAX_PHOTO_BYTES=10485760`.
- `HTTP_READ_HEADER_TIMEOUT=5s`, `HTTP_READ_TIMEOUT=15s`, `HTTP_WRITE_TIMEOUT=30s`, `HTTP_IDLE_TIMEOUT=60s`, `SHUTDOWN_TIMEOUT=20s`.
- `COOKIE_DOMAIN` empty by default. Production validation rejects a value that is not the base URL host or its parent domain.
- `TRUSTED_PROXY_CIDRS` defaults empty locally. Production value is the VPC CIDR/ALB source ranges supplied by Terraform.

## 3. Startup sequence

Implement this exact order in `internal/app`:

1. Parse and validate configuration.
2. Create logger; set default logger.
3. Load `Europe/Lisbon` and fail startup if unavailable.
4. Parse `DATABASE_URL` with `pgxpool.ParseConfig`; set pool limits from configuration.
5. Open pool and perform a ping with a 5-second context deadline.
6. Create sqlc queries and SCS pgx store.
7. Configure SCS cookies and lifetimes.
8. Create AWS SDK configuration and S3 client. Local endpoint resolver is enabled only when `S3_ENDPOINT` is set.
9. Construct storage service, handlers, middleware, and router through explicit constructors; no global mutable dependencies.
10. Start HTTP server.
11. Listen for `SIGINT`/`SIGTERM`; call `Server.Shutdown` with configured timeout; close DB pool after server shutdown; exit non-zero on startup or shutdown failure.

Do not run database migrations from web-process startup.

## 4. Route table

The following table is exhaustive. Static assets are embedded. Do not create JSON APIs or undocumented routes.

| Method | Path | Access | Behaviour |
|---|---|---|---|
| GET | `/` | public | Redirect 303 to `/dashboard` if authenticated, otherwise `/login` |
| GET | `/assets/{path...}` | public | Embedded immutable assets; fingerprinted assets get one-year cache |
| GET | `/health/live` | public | Always 200 after router creation; body `ok\n` |
| GET | `/health/ready` | public | DB ping with 2-second timeout; 200 or 503; no internal detail |
| GET | `/login` | anonymous-only | Login page; authenticated users redirect `/dashboard` |
| POST | `/login` | anonymous-only | Authenticate |
| GET | `/registo` | anonymous-only | Adult registration page |
| POST | `/registo` | anonymous-only | Adult registration |
| POST | `/logout` | authenticated | Destroy session; 303 `/login` |
| GET | `/dashboard` | authenticated | 303 role-specific route |
| GET | `/dashboard/competitor` | Competitor | Competitor dashboard |
| GET | `/dashboard/leisure` | Leisure | Leisure dashboard |
| GET | `/dashboard/guardian` | Guardian | Guardian dashboard |
| GET | `/admin/fleet` | Admin | Fleet/admin dashboard |
| POST | `/admin/maintenance` | Admin | Schedule maintenance; HTMX or normal form |
| POST | `/repairs` | authenticated adult | Submit repair report |
| POST | `/guardian/add-dependent` | Guardian | Add dependent |

Role redirect mapping is exact: Admin → `/admin/fleet`; Competitor → `/dashboard/competitor`; Leisure → `/dashboard/leisure`; Guardian → `/dashboard/guardian`.

A dependent cannot authenticate and therefore never reaches a dashboard directly.

## 5. Middleware order

Apply middleware from outermost to innermost in this order:

1. Panic recovery.
2. Request ID creation/validation and response header.
3. Trusted-proxy remote IP extraction. Trust `X-Forwarded-For` and `X-Forwarded-Proto` only when `RemoteAddr` is inside a configured trusted CIDR.
4. Security headers.
5. Access logging with status, bytes, duration, method, route pattern, request ID, remote IP, user ID when available; never log query strings on login or registration.
6. SCS load-and-save.
7. Gorilla CSRF wrapper.
8. Router.
9. Route-group wrappers inside the router: anonymous-only, authenticated, role authorisation.

Use middleware adapter tests to prove ordering.

## 6. Security headers

Production responses, excluding health endpoints where irrelevant, MUST include:

```text
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
X-Frame-Options: DENY
Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()
Cross-Origin-Opener-Policy: same-origin
Content-Security-Policy: default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; connect-src 'self' https://www.googleapis.com; font-src 'self'; upgrade-insecure-requests
```

Local CSP omits `upgrade-insecure-requests`. No inline script or style is permitted; all JavaScript and CSS must be bundled files.

## 7. Session and CSRF behaviour

- SCS cookie name `mycfc_session`.
- `HttpOnly=true`, `Path=/`, `SameSite=Lax`, `Secure=true` in production.
- Renew token after successful login.
- Store only `user_id`, `role`, and `last_seen_at`; load current user from DB for protected requests.
- Destroy session when user is inactive or missing.
- Gorilla CSRF cookie name `mycfc_csrf`; `Secure` follows environment; `SameSite=Lax`; max age 12 hours.
- Every rendered form includes the hidden token returned by Gorilla CSRF.
- CSRF failure returns rendered `403` with request ID. HTMX receives the same status and an error partial.

## 8. Handler and template conventions

- Handler methods are on an `Application`/handler struct with injected interfaces.
- Parse forms with explicit size limits and call `r.ParseForm`/`ParseMultipartForm` only once.
- Redirect after successful non-HTMX POST to prevent resubmission.
- Successful HTMX POST returns a focused success partial and optionally `HX-Trigger` JSON.
- Use `http.Error` only for health endpoints; normal errors render templates.
- Templates accept typed view models; do not read sessions, environment variables, or database data directly.

## 9. Health semantics

- ALB target health check uses `/health/ready`, interval 30s, timeout 5s, healthy threshold 2, unhealthy threshold 3, matcher 200.
- Liveness does not check dependencies.
- Readiness checks only the database; S3 is intentionally excluded to avoid removing all tasks during an S3 incident.

## 10. Tests and acceptance criteria

- Table-driven tests cover every route/method/access combination.
- Middleware tests verify no protected handler runs without authentication/role/CSRF.
- Spoofed forwarding headers from untrusted addresses are ignored.
- Shutdown completes within the configured timeout and cancels in-flight background contexts.
- Readiness returns 503 when the DB is unavailable and recovers to 200.
- Production config rejects HTTP base URL, local S3 endpoint, insecure cookie mode, malformed hashes, and short CSRF key.
- All error pages and redirects use the response contract in file 00.

<!-- END 02_routing_and_middleware.md -->


---

<!-- BEGIN 03_htmx_dashboards.md -->

# 03 — templ pages, HTMX dashboards and public Google Calendar integration

## 1. Objective

Implement complete role dashboards with typed view models, deterministic database queries, bundled frontend assets, accessible HTMX behaviour, and explicit public-calendar sources.

## 2. Frontend build files

Create and commit:

```text
package.json
package-lock.json
ui/static/src/app.js
ui/static/src/calendar.js
ui/static/src/app.css
ui/static/dist/*
internal/app/assets.go
```

Dependencies: Pico CSS, HTMX, HTMX response-targets extension, FullCalendar core, day-grid, list, interaction, and Google Calendar plugin, plus esbuild as a development dependency. Pin versions through `package-lock.json`; do not load from a CDN.

`npm run build` MUST create fingerprinted/minified production assets and an asset manifest consumed by templates. `npm run dev` may emit source maps; production build must not expose source maps.

## 3. Base templ contract

Create:

- `ui/components/base.templ`
- `ui/components/nav.templ`
- `ui/components/form.templ`
- `ui/components/flash.templ`
- `ui/components/errors.templ`
- `ui/pages/error.templ`

`base.templ` MUST:

- Render `<!doctype html>` and `<html lang="pt-PT">`.
- Set UTF-8 and responsive viewport.
- Include a skip link to `#conteudo-principal`.
- Load only manifest-resolved local CSS/JS with `defer`.
- Render a semantic header/nav/main/footer.
- Render request ID on error pages but not normal pages.
- Include CSRF hidden fields inside each state-changing form, not one detached global input.
- Show authenticated navigation appropriate to role.
- Use a normal POST form for logout.

## 4. Calendar contract

Calendar data is fetched directly by the browser from Google Calendar and therefore may contain only public event data.

- Initialise calendar from `calendar.js`; no inline JavaScript.
- Read API key, timezone and event sources from escaped `data-*` attributes generated by the backend.
- Timezone exactly `Europe/Lisbon`.
- Locale exactly `pt` with custom labels reviewed for pt-PT.
- Default desktop view `dayGridMonth`; narrow viewport defaults to `listMonth`.
- Event source failures show an accessible inline warning and log only a non-sensitive browser-console message.
- Render a non-JavaScript fallback containing links to the public calendars and a message that the interactive calendar needs JavaScript.
- Calendar API key must never be described as confidential. Infrastructure documentation must require HTTP-referrer and Google Calendar API restrictions.

Role event sources:

- Competitor: training + competition calendars.
- Leisure: social + clean-up calendars.
- Guardian: union of sources relevant to the guardian’s dependents. Competitor dependent receives training + competition; Leisure dependent receives social + clean-ups. Deduplicate identical sources.
- Admin: all four calendars.

## 5. Typed dashboard view models

Create explicit types in `internal/handlers/viewmodels.go`; no template accepts database-generated structs directly.

### Common

```go
type PageMeta struct {
    Title       string
    CurrentPath string
    CSRFField   templ.Component
    CurrentUser CurrentUserVM
    Flash       *FlashVM
}
```

### Competitor

- User summary.
- Up to 6 most recent performance metrics from the last 90 days.
- Up to 10 training logs, newest first.
- Relevant WhatsApp groups, active only.
- Calendar configuration.

Empty states must say what is absent without suggesting a broken system.

### Leisure

- Up to 10 published news items, newest first.
- Gallery HTTPS link from validated configuration.
- Relevant WhatsApp groups.
- Social and clean-up calendars.

### Guardian

- All active dependents sorted by name.
- For each dependent: role, squad, age display, and source labels used in schedule.
- Add-dependent form with field values/errors.
- Combined relevant calendars.
- If no dependents exist, show onboarding explanation and the form.

### Admin

- Counts by equipment status.
- Pageless list of all equipment for this MVP, ordered by status/type/name; cap at 500 and show a warning if capped.
- Up to 50 pending repair requests, oldest first.
- Up to 50 upcoming maintenance tasks over the next 90 days.
- Maintenance scheduling form.
- Pre-signed photo URLs expire after 10 minutes and are generated only for rows displayed.

## 6. HTMX conventions

Forms may be submitted by normal navigation or HTMX. Both paths must be complete.

- Use `hx-post`, `hx-target`, `hx-swap="outerHTML"`, `hx-disabled-elt="find button, find input[type='submit']"`.
- Use response-targets so `422` swaps the returned form into the same target.
- Do not use `aria-busy="true"` statically. `app.js` handles `htmx:beforeRequest` and `htmx:afterRequest`/`htmx:responseError`, toggling `aria-busy`, a visible loading label, and disabled state.
- On successful swaps, move focus to a success heading or first invalid field as appropriate.
- Announce dynamic messages through a `role="status" aria-live="polite"` region; errors use `role="alert"`.
- Do not return fragments when the request lacks the `HX-Request: true` header.

## 7. Administrator maintenance form

Fields:

- `equipment_id` required UUID selected from non-retired equipment.
- `scheduled_for` required local datetime; backend parses in Europe/Lisbon and stores timestamptz.
- `description` required, trimmed 10–2000 characters.

POST `/admin/maintenance` creates a `Scheduled` task and changes equipment status to `Maintenance` in one DB transaction when scheduled time is now or earlier; future tasks leave current equipment status unchanged. Return 200 HTMX success partial or 303 to `/admin/fleet` for standard forms. Validation returns 422.

## 8. Data-loading failure behaviour

A dashboard query failure aborts the page and returns 500; do not silently omit failed sections. Failure to create one pre-signed image URL logs a warning and renders “Imagem temporariamente indisponível” for that row without failing the page.

All dashboard queries use request context and a 5-second database deadline.

## 9. Tests and acceptance criteria

- Golden or DOM-based component tests cover populated and empty state for all four dashboards.
- Role dashboards never show navigation or data belonging exclusively to another role.
- Calendar source tests verify exact role mapping and deduplication.
- JavaScript-disabled Playwright tests complete login, navigation, dependent creation and repair submission.
- HTMX tests verify 200 success fragments, 422 form replacement, focus target attributes and normal-form redirects.
- Built HTML contains no inline script/style, no CDN URLs and no unescaped configuration.
- `npm run build` and templ generation are deterministic.

<!-- END 03_htmx_dashboards.md -->


---

<!-- BEGIN 04_equipment_workflow.md -->

# 04 — Repair workflow, multipart validation and private S3 storage

## 1. Objective

Implement an idempotent repair-report flow that safely accepts an optional image, stores it privately, records exact metadata, compensates failed database writes, and works with AWS S3 and local MinIO.

## 2. Storage abstraction

Create `internal/storage/storage.go`:

```go
type ObjectStore interface {
    PutRepairPhoto(ctx context.Context, key, contentType string, size int64, body io.Reader) error
    DeleteObject(ctx context.Context, key string) error
    PresignGet(ctx context.Context, key string, lifetime time.Duration) (string, error)
}
```

Production implementation uses AWS SDK for Go v2 `s3.Client.PutObject`, `DeleteObject`, and the v2 pre-sign client. Do not use the deprecated S3 manager uploader; the accepted object is at most 10 MiB and a single `PutObject` is sufficient.

Put-object requirements:

- Bucket from configuration.
- Server-side encryption `AES256` explicitly requested.
- Content type set from validated sniffing, not browser-provided type.
- Metadata keys: `request-id` and `uploaded-by-user-id`; values contain no personal name/email.
- No ACL header; bucket ownership is enforced by infrastructure.
- Context deadline 30 seconds.

Local implementation uses the same client with endpoint and path-style options.

## 3. Repair form

Create `ui/components/repair_form.templ`. Fields:

- `equipment_id`: required select of active/non-retired equipment.
- `issue_description`: required textarea, 10–2000 trimmed characters.
- `photo`: optional file; accept hint `image/jpeg,image/png,image/webp` but server validation remains authoritative.
- `idempotency_key`: hidden cryptographically random UUID generated when the form is rendered.
- CSRF hidden field.

Form attributes:

```html
method="post"
action="/repairs"
enctype="multipart/form-data"
hx-post="/repairs"
hx-target="this"
hx-swap="outerHTML"
hx-disabled-elt="find button, find input[type='submit']"
```

Do not call browser-side button disabling “idempotency”; server-side uniqueness is the idempotency guarantee.

## 4. Request limits and validation

Handler sequence:

1. Confirm authenticated non-dependent user.
2. Wrap body with `http.MaxBytesReader` using `MAX_REQUEST_BYTES`.
3. Parse multipart with a 1 MiB memory threshold so larger files spill to temporary disk; ensure temporary files are removed.
4. Reject unknown form fields except the CSRF field generated by Gorilla CSRF.
5. Validate UUID idempotency key and equipment ID.
6. Load equipment; reject missing, retired, or inaccessible record with 422 generic field error.
7. Validate description.
8. Validate optional file before S3 upload.

Photo rules:

- Maximum 10 MiB actual bytes; read through `io.LimitReader(max+1)`.
- Only JPEG, PNG or WebP.
- Sniff at least first 512 bytes using `http.DetectContentType` and verify image decoding with `image.DecodeConfig` plus WebP decoder.
- Reject SVG, GIF, BMP, TIFF, archives, polyglot files whose decoder/type disagree, zero-byte files, and image dimensions over 12000×12000.
- Ignore supplied filename except deriving a safe extension from validated content type.
- Object key format: `repairs/YYYY/MM/<uuid>.<jpg|png|webp>` where date is Europe/Lisbon and object UUID is independent from repair/idempotency IDs.
- Never include original filename, user name, equipment name or email in the key.

## 5. Idempotency and write algorithm

1. Before upload, query `GetRepairByIdempotencyKey`.
2. If found and belongs to the same `reported_by_id`, return the same success result without another upload or insert.
3. If found for a different user, return 409 and log a security warning.
4. If no photo: insert directly with null image metadata using `CreateRepairRequest`.
5. If photo exists: upload to S3 first, then insert the DB row.
6. If insert fails because another concurrent request won the unique key, delete the newly uploaded object, load the existing row and return it if owned by the same user.
7. For any other insert failure, synchronously attempt S3 delete with a separate 10-second context. Log delete failure with object key and request ID; return 500.
8. Do not hold a database transaction during upload.

The success response is semantically idempotent even when the original HTTP response was lost.

## 6. Responses

- HTMX success: 200 replacement component titled “Avaria reportada”, contains repair reference ID shortened for display, and a fresh empty form with a new idempotency key.
- Standard success: 303 to the current user’s dashboard with flash message.
- Validation: 422 with form preserving description/equipment selection; file input is never repopulated; tell the user to select the image again.
- Oversized body: 413 with rendered pt-PT error.
- Duplicate key owned by another user: 409 generic conflict.
- Storage/database errors: 500 generic error and request ID.

## 7. Photo display

Only Admin dashboard may display repair photos in this release. Generate pre-signed GET URLs with 10-minute expiration, response content disposition `inline`, and validated content type. Do not persist pre-signed URLs.

## 8. Tests and acceptance criteria

Unit tests use a fake `ObjectStore`; integration tests use MinIO.

Mandatory scenarios:

- No-photo success.
- Valid JPEG/PNG/WebP success with exact stored metadata.
- Invalid MIME, decoder mismatch, SVG, oversized bytes and oversized dimensions rejected before storage call.
- Body > global limit returns 413.
- Database failure after upload calls delete exactly once.
- Concurrent same-user idempotency requests produce one DB row and one retained object.
- Cross-user key collision returns 409.
- Retired/missing equipment is rejected.
- S3 objects are not publicly readable; pre-signed URL works and expires.
- No original filename or personal data appears in logs/object keys.

<!-- END 04_equipment_workflow.md -->


---

<!-- BEGIN 05_auth_and_consent.md -->

# 05 — Authentication, adult registration, guardian/dependent flow and consent audit

## 1. Objective

Implement secure adult accounts, role assignment rules, guardian-owned dependent records, auditable versioned consent, and operational admin credential management without public admin registration.

## 2. Adult self-registration

Routes: `GET /registo`, `POST /registo`.

Form fields:

- `name`: 2–120 Unicode characters after trimming/collapsing internal whitespace.
- `email`: required, trim, parse, lowercase for display consistency; DB citext is authoritative for uniqueness; maximum 254 bytes.
- `date_of_birth`: required ISO date; user must be at least 18 on the Europe/Lisbon current date.
- `password`: 12–72 bytes; must contain at least one letter and one non-letter; do not impose arbitrary composition beyond this.
- `password_confirmation`: exact match.
- `role`: only `Competitor`, `Leisure`, or `Guardian`.
- `squad_category`: required only for Competitor and limited to `Iniciante`, `Polo_Senior`, `Master_A`; forced to `Lazer` for Leisure and `None` for Guardian.
- `accept_terms`: required.
- `accept_image_use`: required by current business rule.
- CSRF token.

Never allow public creation of `Admin` or a dependent account.

POST algorithm:

1. Parse and validate all fields; return 422 with field errors.
2. Hash password with bcrypt cost 12. Reject passwords >72 bytes before hashing.
3. Begin DB transaction.
4. Insert adult user.
5. Insert `Termos_Gerais` and `Uso_Imagem` consent rows using versions/hashes from validated configuration, `user_id = granted_by_user_id`, request IP and truncated user agent.
6. Commit.
7. Renew SCS token, store user ID/role, and redirect to `/dashboard` or return `HX-Redirect: /dashboard`.

On duplicate email, return 422 field error “Já existe uma conta com este endereço de correio eletrónico.” Do not reveal account existence on login, but registration necessarily reports uniqueness.

## 3. Login

Routes: `GET /login`, `POST /login`.

Fields: `email`, `password`, optional safe local `next` path, CSRF token.

Rules:

- Normalise email exactly as registration.
- Load active non-dependent user by email.
- Always perform a bcrypt comparison. When no user exists, compare against a compiled valid dummy bcrypt hash to reduce account-timing differences.
- Generic failure text: “O endereço de correio eletrónico ou a palavra-passe não estão corretos.”
- Add a random server-side delay of 100–250 ms on failed login. This is defence-in-depth; AWS WAF provides distributed rate limiting.
- On success, renew session token, store user ID and role, then redirect to validated `next` or `/dashboard`.
- `next` is accepted only when it begins with one `/`, does not begin `//`, has no scheme/host, and maps to an application path.
- Never log the submitted email at warning/error level; access logs may store a one-way keyed hash only if implemented, otherwise omit it.

## 4. Logout

`POST /logout` destroys the session and redirects 303 to `/login`. GET logout is not implemented. Repeated logout posts are safe.

## 5. Guardian dependent creation

Route: `POST /guardian/add-dependent`; Guardian only.

Form fields:

- `name`: same validation as adult.
- `date_of_birth`: required; person must be under 18 on Europe/Lisbon current date and not in the future.
- `role`: only `Competitor` or `Leisure`.
- `squad_category`: same mapping as registration.
- `accept_minor_responsibility`: required.
- CSRF token.

The form has no email or password fields. A dependent cannot log in.

POST algorithm:

1. Load current guardian from DB and verify active `Guardian`, not merely session role.
2. Enforce a maximum of 10 active dependents per guardian; return 422 when reached.
3. Validate form.
4. Transactionally insert dependent with `guardian_id` current user, `is_dependent=true`, null credentials, and insert `Responsabilidade_Menor` consent concerning the dependent but granted by the guardian.
5. Commit.
6. Return 200 HTMX success + refreshed dependent list/form, or 303 `/dashboard/guardian` with flash.

Never accept guardian ID from the browser.

## 6. Consent audit requirements

- Versions and SHA-256 hashes come only from configuration, never hidden form fields.
- The rendered label links to the exact legal document URL/version controlled by the organisation. Add configuration-backed URLs if the final legal documents are hosted separately; they must be HTTPS in production.
- Store request IP only after trusted-proxy processing.
- Truncate user agent to 512 characters.
- Consent rows are append-only.
- The application must expose no consent-edit route in this release.
- Image-use consent being mandatory is a business requirement, not a claim of legal necessity; preserve it exactly unless the product owner changes the specification.

## 7. Authorisation helpers

Create helpers that load current user from DB and compare database role. Session role may optimise redirects but is not authoritative.

- Admin: `/admin/*` only.
- Guardian: dependent creation and guardian dashboard.
- Competitor/Leisure: own dashboards.
- Any active adult role: repair submission.
- Dependent/inactive/missing users: session destroyed and treated as unauthenticated.

## 8. Admin CLI

Create `cmd/admin` with subcommands:

```text
admin create --email ... --name ... --date-of-birth YYYY-MM-DD
admin set-password --email ...
admin deactivate --email ...
```

Passwords are read twice from a TTY or from `MYCFC_ADMIN_PASSWORD_FILE`; never command-line flags or environment variables. The command uses the same validation/hash code and is idempotent where safe. It must refuse non-TTY input without the password-file option.

No default administrator or seed password is created by migrations.

## 9. Tests and acceptance criteria

- Adult boundary tests around eighteenth birthday in Europe/Lisbon.
- Dependent boundary tests around eighteenth birthday and future dates.
- All role/squad invalid combinations return 422 and cannot bypass DB constraints.
- Registration rollback leaves neither user nor consents when either consent insert fails.
- Dependent rollback leaves neither dependent nor consent.
- Passwords over 72 bytes are rejected without bcrypt truncation.
- Login error is identical for unknown email, wrong password, inactive account and dependent record.
- Session token changes after login; logout invalidates it.
- Open redirect attempts are rejected.
- Guardian cannot create an 11th active dependent.
- Non-Guardian and spoofed guardian IDs cannot create dependents.
- Admin cannot be self-registered.

<!-- END 05_auth_and_consent.md -->


---

<!-- BEGIN 06_frontend_a11y_pt_PT.md -->

# 06 — WCAG 2.2 AA and European Portuguese localisation

## 1. Objective

All rendered interfaces must meet WCAG 2.2 Level AA for the implemented flows and use natural European Portuguese. Accessibility is a tested behavioural requirement, not a collection of decorative ARIA attributes.

## 2. Language and formatting

- Root language: `<html lang="pt-PT">`.
- Use `golang.org/x/text/language`/`message` or explicit tested formatters for pt-PT numbers.
- Display dates as `02/01/2006`, concise dates such as `2 de janeiro de 2006`, and times in 24-hour format, according to context.
- Convert all application times to `Europe/Lisbon` before rendering. Include timezone abbreviation when ambiguity matters.
- Database enum values are never shown directly; map them to approved pt-PT labels.
- Avoid untranslated English except proper product names such as WhatsApp.

## 3. Mandatory glossary

| Internal/English concept | Required pt-PT text |
|---|---|
| Dashboard | Painel |
| Report repair | Reportar avaria |
| Repair request | Pedido de reparação |
| Paddles | Pagaias |
| Boats | Embarcações |
| Vehicle | Viatura |
| Newsfeed | Notícias |
| Username | Utilizador (only where a username exists) |
| Email | Correio eletrónico |
| Password | Palavra-passe |
| Login | Iniciar sessão |
| Logout | Terminar sessão |
| Guardian | Tutor |
| Dependent/minor | Menor a cargo |
| Submit | Enviar |
| Cancel | Cancelar |
| Pending | Pendente |
| Under review | Em análise |
| Resolved | Resolvido |
| Maintenance | Manutenção |
| Training | Treino |
| Clean-up event | Ação de limpeza |

Do not use pt-BR terms such as “usuário”, “senha”, “cadastro”, “arquivo” for uploaded file, “time” for team, or gerund-heavy UI wording.

## 4. Structural accessibility

Every page MUST have:

- Unique descriptive `<title>`.
- One `<h1>` and logical non-skipping heading order.
- Skip link visible on focus.
- Semantic header, nav, main and footer landmarks.
- Current navigation item marked with `aria-current="page"`.
- Visible keyboard focus meeting WCAG 2.2 focus appearance requirements.
- No positive `tabindex`.
- No click-only non-button elements.
- A useful page when CSS or JavaScript fails.

## 5. Forms

- Every input has a visible `<label for>` matching a unique ID.
- Required fields communicate required state in text and `required`; do not rely on an asterisk alone.
- Help text and errors use stable IDs referenced by `aria-describedby`.
- Invalid fields set `aria-invalid="true"`.
- On 422, render a top error summary with `role="alert"`, heading “Corrija os seguintes campos”, and links to invalid inputs.
- Place keyboard focus on the error-summary heading after full-page or HTMX validation response.
- Preserve non-sensitive values. Never echo passwords or file paths.
- Error text identifies the field and corrective action; colour is not the only signal.
- Autocomplete tokens: `name`, `email`, `bday`, `current-password`, `new-password` as applicable.

## 6. HTMX dynamic behaviour

- Before a request, set `aria-busy="true"` on the form/region and replace submit text with a meaningful loading label while preserving button width where practical.
- After completion or error, remove `aria-busy` and restore text/enabled state.
- Success notifications use `role="status" aria-live="polite"`.
- Validation/server errors use `role="alert"`.
- After swaps, focus success heading, error summary, or first new meaningful heading; never reset focus to document body without purpose.
- Respect `prefers-reduced-motion`; no required information depends on animation.

## 7. Visual requirements

- Text and form-control contrast >= 4.5:1; large text >= 3:1; UI components/focus indicators >= 3:1.
- Browser zoom to 200% must not cause two-dimensional scrolling at 1280 CSS pixels except the calendar/table where an accessible alternative is provided.
- Touch targets are at least 24×24 CSS pixels, preferably 44×44.
- Validation red must be paired with icon/text and tested in light/dark browser schemes if both are supported.
- Do not override user font size.

## 8. Calendar and tables

- FullCalendar is enhancement, not the only schedule representation. Provide a “Ver calendários públicos” list fallback.
- Calendar controls have pt-PT accessible names and keyboard operation.
- Fleet table uses `<caption>`, correct column headers and responsive strategy. On small screens it may become cards, but label/value associations must remain programmatic.
- Status is represented by text, not colour alone.

## 9. Images

- Decorative images have empty alt.
- Repair evidence photos use concise contextual alt such as “Fotografia da avaria na embarcação CFC-012”; do not repeat surrounding caption.
- Never use filename as alt text.

## 10. Automated and manual tests

Create Playwright tests with `@axe-core/playwright` for every page state: empty, populated, validation failure, success, 403, 404 and 500.

Automated acceptance:

- No axe serious or critical violations.
- HTML validator has no duplicate IDs or invalid label references.
- Keyboard-only tests can complete every form and dashboard navigation.
- At 320 CSS-pixel viewport, no page-wide horizontal overflow.
- 200% zoom flow remains operable.
- Reduced-motion setting produces no animated scrolling.
- Snapshot/string tests reject known pt-BR banned terms in rendered UI.

Manual checklist is documented in `docs/accessibility-manual-check.md` and covers screen-reader announcements, focus order, calendar fallback, error recovery and contrast. Automated results do not waive manual review.

<!-- END 06_frontend_a11y_pt_PT.md -->


---

<!-- BEGIN 07_aws_deployment_gitops.md -->

# 07 — AWS production infrastructure as code

## 1. Objective

Provision a deployable, secure and observable AWS production environment using Terraform. The target is ECS Fargate behind ALB/WAF, with RDS PostgreSQL, private S3, private task networking, ECR, Route 53, ACM, VPC endpoints, least-privilege IAM and GitHub OIDC.

AWS App Runner MUST NOT be provisioned. It is unavailable to new AWS customers after 31 March 2026.

`architecture_runtime.svg` is the normative visual for the running AWS topology. `architecture_delivery_pipeline.svg` shows ownership and delivery orchestration but intentionally collapses this runtime topology into one boundary.

## 2. Terraform structure and state

```text
infra/bootstrap/                    # state bucket; initially local state
infra/environments/production/      # production stack using S3 backend
```

### Bootstrap

Create a globally unique S3 state bucket with:

- Versioning enabled.
- Default SSE-S3 or customer-managed KMS encryption.
- Public-access block and bucket-owner-enforced ownership.
- TLS-only bucket policy.
- Lifecycle retaining non-current state versions for at least 90 days.
- Deletion protection through `prevent_destroy = true`.

Do not create DynamoDB locking. Production backend uses S3 native lock file:

```hcl
backend "s3" {
  key          = "mycfc/production/terraform.tfstate"
  use_lockfile = true
  encrypt      = true
}
```

Backend bucket/region are supplied through partial backend configuration, not hardcoded secrets. Commit `.terraform.lock.hcl` and constrain Terraform/provider versions.

## 3. Network

Use three availability zones when the selected region supports them.

- VPC CIDR default `10.42.0.0/16`.
- Three public ALB subnets, one per AZ.
- Three private application subnets, one per AZ.
- Three isolated database subnets, one per AZ.
- Internet Gateway and public route tables only for ALB subnets.
- ECS tasks receive no public IP.
- No NAT Gateway. Private AWS access is through VPC endpoints.

VPC endpoints:

- Gateway: S3 attached to application route tables.
- Interface with private DNS: ECR API, ECR DKR, CloudWatch Logs, SSM, Secrets Manager, KMS, STS, ECS, ECS Agent and ECS Telemetry where region supports them.
- Endpoint security group allows TCP 443 only from application subnets/task SG.
- Endpoint policies restrict ECR repositories, S3 bucket and secret/parameter ARNs where the service supports policy restrictions.

## 4. Security groups

- ALB SG: inbound 80/443 from IPv4 and IPv6 internet; outbound only to ECS task SG on container port 8080.
- ECS task SG: inbound 8080 only from ALB SG; outbound 5432 to RDS SG, 443 to endpoint SG, and 443 to S3 prefix list. No unrestricted `0.0.0.0/0` egress.
- RDS SG: inbound 5432 only from ECS task SG and migration task SG if separate.
- Interface endpoint SG: inbound 443 only from ECS task/migration SG.

Use security-group references rather than CIDRs where possible.

## 5. DNS, TLS, ALB and WAF

- Request an ACM public certificate for `domain_name` and `www.domain_name` in the deployment region.
- Validate through Route 53 DNS records managed by Terraform.
- Public ALB spans all public subnets.
- Listener 80 redirects permanently to HTTPS 443.
- Listener 443 uses the ACM certificate, modern AWS recommended TLS security policy, and forwards to an IP target group on port 8080.
- Route 53 A and AAAA alias records point apex and `www` to ALB.
- Application middleware redirects `www` to canonical apex with 308.
- Target health path `/health/ready` with thresholds specified in file 02.
- Enable ALB deletion protection and access logs to a dedicated private log bucket with lifecycle retention.

Attach AWS WAF v2 Web ACL with:

- AWS managed common rule set.
- Known-bad-inputs rule set.
- Amazon IP reputation rule set.
- Rate-based rule for `/login` and `/registo` at a conservative per-IP threshold.
- Separate higher threshold for all requests.
- CloudWatch metrics and sampled requests enabled; do not log request bodies or sensitive headers.

## 6. ECR and image policy

- Private ECR repository with immutable tags.
- Scan on push enabled.
- AES-256 encryption.
- Lifecycle: retain last 30 tagged release images; expire untagged images after 7 days.
- ECS task definition uses image digest, never a mutable tag.

## 7. ECS Fargate

- ECS cluster with Container Insights enabled.
- Fargate Linux x86_64 unless the Docker build is explicitly changed everywhere to ARM64.
- Application task: 0.5 vCPU / 1 GiB default; configurable.
- Desired count 2 across AZs; autoscaling min 2, max 6 based on CPU 60% and ALB requests per target.
- Rolling deployment minimum healthy 100%, maximum 200%.
- Deployment circuit breaker enabled with automatic rollback.
- Enable execute-command only if audit/logging and IAM are configured; default false.
- Read-only root filesystem, non-root user, drop all Linux capabilities, no privileged mode.
- Container health check calls local `/health/live`.
- Stop timeout aligns with application shutdown timeout plus safety margin.
- CloudWatch log group retention 30 days, encrypted, with `prevent_destroy` optional via variable.

Terraform owns the cluster, service and two distinct base task-definition families:

- `mycfc-app`: attached to the ECS service, default command `serve`, application task role, application DB secret and repair-image S3 permissions.
- `mycfc-migrate`: never attached to a service, default command `migrate up`, migration task role and migration/admin DB secret, with no S3 permissions unless a migration explicitly requires them.

GitHub Actions registers one image-specific revision of each family for a release. The ECS service has lifecycle ignore only for the **application** task-definition revision and desired count managed by autoscaling; do not ignore network, IAM or security configuration. The migration family is invoked only as a one-off task and must never inherit the application task role or application DB credentials.

## 8. Database

RDS PostgreSQL 16:

- Instance class default `db.t4g.micro`, configurable.
- Multi-AZ enabled.
- 20 GiB gp3 encrypted storage; autoscaling maximum 100 GiB.
- Private DB subnet group only; not publicly accessible.
- Backup retention 14 days; copy tags to snapshots.
- Deletion protection enabled; final snapshot required; skip-final-snapshot false.
- Auto minor-version upgrade enabled in defined maintenance window.
- Performance Insights enabled with 7-day retention if supported by class.
- Enhanced monitoring at 60 seconds with dedicated role.
- Parameter group sets timezone `Europe/Lisbon`, `log_min_duration_statement=1000`, and safe connection logging without statement/password leakage.
- Master credentials managed by RDS/Secrets Manager; application does not use the master account.

Create an application DB credential secret and a migration/admin credential secret. Migration bootstrap SQL creates/updates least-privileged application role. App runtime role gets only app credential; migration task gets migration credential. Secret values are marked sensitive and never output.

Coordinate DB capacity with ECS: `DB_MAX_CONNS=8` and max six tasks means at most 48 app connections plus migration/admin headroom. Terraform validates this against a documented `db_connection_budget` variable.

## 9. Application S3 bucket

- Private bucket with public-access block and bucket-owner-enforced ownership.
- Versioning enabled.
- Default SSE-S3; bucket policy denies unencrypted transport and non-approved principals.
- No CORS required because uploads/download-signing are server-side.
- Lifecycle aborts incomplete multipart uploads after 7 days and moves non-current versions to expiration according to a documented retention policy.
- Application task role: `PutObject`, `DeleteObject`, `GetObject` only under `repairs/*`, plus `ListBucket` restricted to prefix when required.
- No public ACL or website hosting.

## 10. Runtime secrets and configuration

Use Secrets Manager for DB credentials and CSRF key. Use SSM Parameter Store for non-secret but centrally managed configuration if desired. ECS task definition injects secrets by ARN and regular variables explicitly.

Never store a composed `DATABASE_URL` in Terraform output. The app may build it from injected `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, and `DB_PASSWORD`; update file 02 implementation to support this production form while retaining `DATABASE_URL` for local/test. Exactly one mode must validate as complete.

Task role and execution role are separate:

- Execution role: pull only the project ECR image, write only its log group, read only referenced startup secrets.
- Task role: S3 repair prefix and no infrastructure mutation.
- Migration task role: migration/admin secret access plus DB network; no S3 unless migration code demonstrably needs it.

## 11. GitHub OIDC roles

Create GitHub OIDC provider once per account when not already managed. Trust policy MUST require:

- Audience `sts.amazonaws.com`.
- Subject exactly `repo:<github_org>/<github_repo>:environment:<github_environment>`.
- No wildcard organisation/repository.

Separate roles:

1. `github-infra-plan`: read-only plus state read/lock for pull-request plans; no apply.
2. `github-infra-apply`: scoped permissions necessary for Terraform production stack and state.
3. `github-deploy`: ECR push, task-definition register/pass only approved roles, ECS run migration task, describe/wait/update only the named cluster/service, read needed Terraform outputs/state if chosen.

Apply permissions boundaries where the account supports them. All `iam:PassRole` resources and conditions are explicit.

## 12. Observability and alarms

Create dashboards/alarms for:

- ALB unhealthy hosts, 5xx rate, target response time.
- ECS service running task count below desired, CPU/memory high, deployment failure events.
- RDS CPU, free storage, connections, replica/multi-AZ events and database errors.
- WAF blocked-rate anomaly.
- S3 4xx/5xx if request metrics enabled.

Create EventBridge rule for failed ECS deployments and route to SNS. Optional email subscription remains pending until operator confirms it; Terraform must not claim confirmation.

## 13. Terraform quality gates

Mandatory:

- `terraform fmt -check -recursive`.
- `terraform validate`.
- `tflint` with AWS plugin.
- `checkov` or `tfsec` pinned in CI with documented narrow suppressions only.
- `terraform plan -detailed-exitcode` for PR.
- No secrets in plan artifacts uploaded to untrusted contexts.
- Resource names/tags include project/environment/managed-by/repository.
- Critical resources have `prevent_destroy` where operationally appropriate.

## 14. Acceptance criteria

- A fresh supported AWS account can deploy the stack without App Runner eligibility.
- Only ALB and public DNS are internet-facing.
- ECS tasks have no public IP and can pull ECR, emit logs, read required secrets, reach S3 and RDS without NAT.
- Internet cannot connect directly to task or RDS ports.
- S3 anonymous read fails.
- OIDC token from another repository, branch-only subject or environment is denied.
- Failed ECS deployment automatically rolls back.
- Application and migration task-definition families have distinct commands, roles and secrets; only `mycfc-app` is attached to the service.
- RDS deletion is blocked until protection is deliberately disabled and a final snapshot is selected.
- Terraform second apply produces no unexpected changes after CI has deployed a new task-definition revision.

<!-- END 07_aws_deployment_gitops.md -->


---

<!-- BEGIN 08_github_actions_pipeline.md -->

# 08 — GitHub Actions CI, infrastructure GitOps and production deployment

## 1. Objective

Create reproducible workflows that verify all generated/code/infrastructure artefacts, authenticate to AWS only through OIDC, build one immutable image, run backward-compatible migrations as an ECS one-off task, deploy by digest, and verify/roll back failures.

All third-party actions MUST be pinned to full commit SHA with an adjacent comment naming the human-readable release. Dependabot is configured to update action SHAs, Go modules, npm and Docker dependencies.

`architecture_delivery_pipeline.svg` is the normative visual for this workflow. It references `architecture_runtime.svg` rather than duplicating the production runtime topology.

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
    - `make db-provision`.
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
8. Fetch the current `mycfc-app` and `mycfc-migrate` task-definition JSON documents. Register one new revision of each, changing only image digest, app version/Git SHA and permitted release metadata. Preserve each family’s distinct command, task role, execution role and secret references. Strip read-only AWS fields before registration.
9. Run the new `mycfc-migrate` revision as a one-off Fargate task with command override `["migrate", "up"]`, private application subnets and the migration security group.
10. Wait `tasks-stopped`. Query `stopCode`, `stoppedReason`, container `exitCode` and reason. Fail unless the migration container exit code is exactly 0. Print bounded migration logs on failure without secret values.
11. Only after migration success, update the ECS service to the new `mycfc-app` revision with force-new-deployment.
12. Wait for services stable with an explicit maximum polling duration. Then confirm the primary deployment is completed and all registered targets are healthy.
13. Smoke test `https://<domain>/health/ready`, login GET, and a versioned static asset response. Retry boundedly to tolerate ALB registration and rolling deployment.
14. Write a deployment summary containing SHA, image digest, both task-definition revisions and migration task ARN—not secrets.

If service deployment fails, ECS circuit breaker performs rollback. Workflow reports the rolled-back status and fails. Do not automatically run down migrations.

## 6. Migration compatibility policy

Every normal migration MUST be backward-compatible with the currently deployed release:

- Add nullable columns or columns with safe defaults before code depends on them.
- This reset-only baseline is replaced as a whole; it has no incremental migration or `NO TRANSACTION` convention.
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
3. Final distroless/static non-root image. Copy CA certificates, timezone data if required, binary and no source/build tools. User non-root; read-only filesystem compatible; `ENTRYPOINT ["/app/mycfc"]` and `CMD ["serve"]` so the migration family can safely override the command with `["migrate", "up"]`.

The application binary supports `serve` only. Schema provisioning is a local `psql` operation against a reset database.

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
- A non-zero `mycfc-migrate` task exit prevents updating the ECS service to the new application revision.
- Both application and migration task-definition revisions use the same immutable image digest and can prove which commit produced it.
- An unhealthy image triggers ECS rollback and the workflow fails.
- Re-running deployment for the same SHA is safe: migration is already applied, new task revision may be registered, resulting service image digest is unchanged.
- No workflow has `permissions: write-all`.
- All action references are 40-character SHAs.
- CI catches generated-code drift, schema/query compile errors, race failures, a11y serious/critical issues and high/critical image vulnerabilities.

<!-- END 08_github_actions_pipeline.md -->


---

<!-- BEGIN 09_local_dev_and_minio.md -->

# 09 — Deterministic local development with PostgreSQL, MinIO and Air

## 1. Objective

A fresh clone must reach a working local application using documented commands, without manually creating databases, users or buckets. Local behaviour must exercise the pgx, sqlc and S3 code paths used by the application.

This file is the only canonical local-development specification; no CI document may redefine its Make targets.

## 2. Files

```text
compose.yaml
.env.example
.air.toml
Makefile
scripts/wait-for-http.sh
scripts/local-bootstrap.sh
```

Never commit `.env`, local data, MinIO credentials outside `.env.example`, or generated temporary uploads.

## 3. Compose services

Pin image versions/digests and include health checks.

### `postgres`

- PostgreSQL 16 Alpine.
- Port `127.0.0.1:5432:5432`.
- Database `mycfc`, user `mycfc`, password `mycfc_local_only`.
- `TZ=Europe/Lisbon`, PostgreSQL option `timezone=Europe/Lisbon`.
- Named volume `pgdata`.
- Health check `pg_isready -U mycfc -d mycfc`.
- Stop grace period 30 seconds.

### `minio`

- MinIO server with console.
- Ports bound to loopback: 9000 API, 9001 console.
- Root user `mycfc_local`, root password `mycfc_local_password_change_not_prod` in `.env.example` only.
- Command `server /data --console-address :9001`.
- Named volume `miniodata`.
- Health check against `/minio/health/live`.

### `minio-init`

Use pinned `minio/mc`. Wait for MinIO health, configure alias, create bucket `mycfc-local` idempotently, set anonymous access to none, and exit 0. A rerun must be safe.

Compose has a named network and no application container by default; Go/air runs on host for fast development. CI may use the same Compose services.

## 4. `.env.example`

Include every configuration variable from file 02 with safe local values:

```dotenv
APP_ENV=local
APP_VERSION=dev
GIT_SHA=0000000000000000000000000000000000000000
PORT=8080
BASE_URL=http://localhost:8080
DATABASE_URL=postgres://mycfc:mycfc_local_only@localhost:5432/mycfc?sslmode=disable
CSRF_AUTH_KEY_B64=<document command to generate; example must decode to 32 non-production bytes>
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=mycfc_local
AWS_SECRET_ACCESS_KEY=mycfc_local_password_change_not_prod
S3_BUCKET_NAME=mycfc-local
S3_ENDPOINT=http://localhost:9000
S3_FORCE_PATH_STYLE=true
GOOGLE_CALENDAR_API_KEY=replace-with-restricted-local-browser-key
CALENDAR_COMPETITION_ID=replace-with-public-calendar-id
CALENDAR_TRAINING_ID=replace-with-public-calendar-id
CALENDAR_SOCIAL_ID=replace-with-public-calendar-id
CALENDAR_CLEANUPS_ID=replace-with-public-calendar-id
GALLERY_URL=https://example.invalid/gallery
CONSENT_TERMS_VERSION=dev-v1
CONSENT_TERMS_SHA256=<64 lowercase zeroes allowed only in local env>
CONSENT_TERMS_URL=http://localhost:8080/legal/termos-gerais
CONSENT_IMAGE_VERSION=dev-v1
CONSENT_IMAGE_SHA256=<...>
CONSENT_IMAGE_URL=http://localhost:8080/legal/uso-imagem
CONSENT_MINOR_VERSION=dev-v1
CONSENT_MINOR_SHA256=<...>
CONSENT_MINOR_URL=http://localhost:8080/legal/responsabilidade-menor
```

Application startup in local mode may accept `.invalid` URLs and documented zero hashes; production validation must reject them.

## 5. Make targets

These names and meanings are canonical:

```text
make tools              install pinned Go tools into ./bin
make dev-infra          docker compose up -d --wait postgres minio minio-init
make dev-infra-down     docker compose down
make dev-infra-clean    docker compose down -v (requires interactive confirmation unless CI=true)
make generate           templ generate; sqlc generate; npm run build
make db-provision       provision the reset-only baseline into an empty local database
make db-provision-test  recreate and provision mycfc_test from the baseline
make dev-bootstrap      copy .env.example to .env if absent, start infra, provision, build assets
make dev                load .env and run ./bin/air
make test               unit tests
make test-integration   starts infra and runs tagged/integration suite
make test-e2e           builds app and runs Playwright/axe
make verify             format check, generate-diff check, vet, tests, frontend build/audit, Terraform checks
make reset-local        clean volumes then bootstrap; interactive confirmation
```

Targets use bash with `.SHELLFLAGS := -Eeuo pipefail -c`. Commands fail immediately; do not prefix failures with `-` or `|| true` except cleanup traps.

## 6. Air configuration

Air watches `.go`, `.templ`, `.sql`, `.js`, `.css`, `go.mod`, `package*.json`, and excludes generated output/temp directories.

On change, Air runs one deterministic build command:

```text
make generate-fast && go build -o .tmp/mycfc ./cmd/server
```

`generate-fast` runs templ/sqlc and esbuild incremental build as needed. It must not start separate orphan watcher processes. Air then runs `.tmp/mycfc`. Stop signals must reach the Go child process.

Do not use `go run` for `make dev`.

## 7. Local S3 parity

- Same `ObjectStore` implementation and S3 API calls as production.
- Force path style only when configured.
- Integration tests verify private bucket, upload, delete and pre-sign.
- Tests create unique prefixes and clean them in `t.Cleanup`.
- No test assumes MinIO-specific response text.

## 8. Database test isolation

Integration tests use a separate database `mycfc_test` created by bootstrap or create a per-test schema. Parallel tests must not share mutable rows unless designed for concurrency testing. Migrations run before tests.

## 9. Documentation

Root `README.md` includes exactly:

```bash
cp .env.example .env
# fill Google Calendar development values
make tools
make dev-bootstrap
make dev
```

Also document MinIO console URL, local credentials warning, migration commands, reset command and how to run verification.

## 10. Acceptance criteria

- Fresh clone on supported Linux/macOS with Go, Node, Docker and Make reaches healthy app via commands above.
- `docker compose up -d --wait` is reliable and idempotent.
- Bucket is auto-created and remains private.
- Data persists across `dev-infra-down` and is removed only by clean/reset.
- Air rebuilds after Go, templ and SQL edits; sqlc/templ errors stop the build and display clearly.
- Local upload/presign flow passes against MinIO.
- All Make targets have help text and no duplicated/conflicting migration target names.

<!-- END 09_local_dev_and_minio.md -->


---

<!-- BEGIN 10_acceptance_test_matrix.md -->

# 10 — Final acceptance test matrix and definition of completion

## 1. Purpose

This is the release gate. The coding agent MUST implement and run every mandatory check. A visually plausible scaffold is not completion.

## 2. Mandatory static commands

From a clean checkout after dependencies are installed:

```bash
make dev-infra
make db-provision
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
dot -Tsvg architecture_runtime.dot -o /tmp/architecture_runtime.svg
dot -Tsvg architecture_delivery_pipeline.dot -o /tmp/architecture_delivery_pipeline.svg
```

`make verify` MUST run the applicable superset and fail on the first failing phase with a clear label.

## 3. Database scenarios

| ID | Scenario | Expected result |
|---|---|---|
| DB-01 | Fresh PostgreSQL 16, `psql -f internal/db/schema.sql` | Complete baseline applies |
| DB-02 | Reset local database and provision again | Complete baseline reapplies |
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

## 11. Architecture documentation scenarios

| ID | Scenario | Expected result |
|---|---|---|
| DOC-01 | Render both `.dot` sources with Graphviz | Both SVGs generate without syntax errors |
| DOC-02 | Inspect runtime diagram scope | Running components, trust boundaries and request/data flows only; no GitHub Actions, Terraform state or deployment migration orchestration |
| DOC-03 | Inspect delivery diagram scope | CI, OIDC, Terraform, distinct app/migration task families, migration gate, service update and rollback are shown; runtime topology is collapsed to one referenced boundary |
| DOC-04 | Cross-check files 00, 07, 08 and README | Diagram names, task-family semantics and scope rules agree exactly |

## 12. Evidence required

The final implementation PR/agent report MUST include:

- Command summary with pass/fail counts.
- Integration and E2E test report paths.
- Axe report.
- Container SBOM and vulnerability summary.
- Terraform plan summary with sensitive values redacted.
- Deployed image digest for production deployment runs.
- List of deliberately deferred product features, which may include password reset/email delivery, consent revocation UI, user profile editing and admin content authoring. Deferred items must not be represented by active dead-end controls.

## 13. Hard completion blockers

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
- Runtime and delivery concerns are recombined into one ambiguous architecture diagram or their task-family semantics disagree with files 07/08.
- Unresolved architecture decisions remain in TODO comments.

<!-- END 10_acceptance_test_matrix.md -->

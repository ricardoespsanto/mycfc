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
| Data access | `sqlc` using `sql_package: pgx/v5`; handwritten SQL outside migrations/queries is forbidden |
| Migrations | `pressly/goose/v3`; migrations are forward-only in production |
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
10. When third-party API behaviour differs from this specification, use the official current API, preserve the intended security property, and document the exact adaptation in `IMPLEMENTATION_NOTES.md`.

## 9. Global definition of done

- All commands and scenarios in file 10 pass.
- `git grep -nE 'TODO|FIXME|CHANGEME|example\.com|<YOUR_|dummy|insecure'` returns no production-code findings except explicitly allowed examples in `*.example` files and documentation.
- Fresh clone + documented local bootstrap works without manual database or bucket operations.
- Terraform formatting, validation and static checks pass.
- GitHub workflows contain no long-lived cloud credentials and all third-party actions are pinned by full commit SHA.
- Production image runs as a non-root user, has no shell/package manager in the final stage, and exposes only port 8080.

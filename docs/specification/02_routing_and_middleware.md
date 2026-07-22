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

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

# 01 — PostgreSQL schema, migrations, sqlc and transactional contracts

## 1. Objective

Create a complete PostgreSQL 16 schema and generated data-access layer that can support every route and dashboard without ad-hoc SQL or invented fields.

## 2. Files to create

```text
internal/db/migrations/00001_extensions_and_types.sql
internal/db/migrations/00002_core_schema.sql
internal/db/migrations/00003_dashboard_schema.sql
internal/db/migrations/00004_sessions.sql
internal/db/queries/users.sql
internal/db/queries/consents.sql
internal/db/queries/equipment.sql
internal/db/queries/repairs.sql
internal/db/queries/dashboards.sql
internal/db/queries/maintenance.sql
internal/db/queries/whatsapp.sql
sqlc.yaml
```

Each migration MUST contain Goose `Up` and `Down` sections. Production automation runs only `goose up`; down migrations exist for local development and tests.

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
- Schema points to migrations; queries point to `internal/db/queries`.
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

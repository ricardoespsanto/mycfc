# Privacy retention maintenance

Status: source-complete and disabled by default. This procedure does not authorize a live role grant, timer activation, lifecycle apply, legacy purge, privacy-request activation, or production deployment.

## Boundaries

Migration `202609100010_privacy_retention_completion` adds the approved clocks without inventing unresolved #109 facts:

- consent evidence becomes inactive on supersession, withdrawal, processing cessation, or account erasure and expires exactly three years after cessation; its IP address and user agent are independently scrubbed after 12 months;
- the eight already-inventoried actor/subject audit families are pseudonymised row by row after 24 months, so a newer row for the same person remains attributable until its own deadline;
- a tracked repair attachment enters exact-version cleanup after 23 days, leaving seven days for the existing version-aware worker to record authoritative `ABSENCE_VERIFIED` evidence before day 30;
- existing technical-retention work remains bounded to one batch per invocation.

An expired consent row referenced by a current profile photo is retained instead of aborting the batch. New profile pointers must reference a matching active `Foto_Perfil` consent, and the locked consent candidate prevents a concurrent reference from racing evidence deletion. Operators must investigate any referenced expired consent as a data-lifecycle backlog; they must not remove the foreign key or bypass the invariant.

The S3 lifecycle rule expires current objects under `repairs/` after 30 days and noncurrent versions after one day. S3 lifecycle is asynchronous and is only a backstop. The protected cleanup event and exact-version absence evidence are the authoritative day-30 proof.

## Least-privilege database identity

Database bootstrap creates `mycfc_privacy_retention` as `NOLOGIN`. It has no direct table or sequence privileges and can execute only `privacy_retention_run(uuid,integer)` and `privacy_retention_status()`. Provision a separate login through the production secret process, grant only membership in that fixed capability role, and put its PostgreSQL URL in `/etc/mycfc/privacy-retention.env`. Do not reuse the web, migration, bootstrap, privacy-executor, or backup identity.

The root-owned file must be mode `0600` and contain only:

```text
PRIVACY_RETENTION_DATABASE_URL=postgres://<dedicated-login>:<secret>@postgres:5432/<database>?sslmode=disable
PRIVACY_RETENTION_WORKER_REF=<stable-random-uuid>
PRIVACY_RETENTION_BATCH_LIMIT=500
```

The command refuses a missing gate, credential URL, non-UUID worker reference, or batch outside 1–10,000. It uses `SET LOCAL ROLE mycfc_privacy_retention`, verifies the effective role, runs one transaction, and emits counts and ages only. It never emits a database URL, object key, user ID, request ID, consent ID, or audit content.

## Inactive rollout and monitoring

Keep this exact line in `/etc/mycfc/mycfc.env` until a separate live-system approval:

```text
PRIVACY_RETENTION_ENABLED=false
```

`deployment/install.sh` installs but disables `mycfc-privacy-retention.timer` while that gate is false. Once separately approved, validate a reviewed Terraform plan, provision the dedicated login and file, set the gate to exact lowercase `true`, rerun the installer, and confirm the hourly timer. The service sends its bounded output through the existing `/mycfc/production/deployment` CloudWatch path.

`privacy_retention_succeeded` records only per-category counts, remaining due count, oldest due age, repair due/overdue/failure/legacy counts, and last-run age. Any `privacy_retention_failed`, `privacy_retention_backlog_breach`, or `privacy_retention_sla_breach` event raises the existing privacy alert topic in the next one-minute period. A legacy repair pointer due for cleanup is an alarm, not a guessed deletion target.

## Failure and compensation

The database work is one transaction and one advisory-lock holder. A failed category rolls back the entire run and records no false success. Investigate the privacy-safe event, database availability, role membership, due/age status, and existing protected cleanup evidence; never edit immutable events or fabricate absence evidence.

Migration rollback is forward-only compensation: keep cessation anchors, pseudonymous principals, run evidence, cleanup jobs, and lifecycle-safe columns. Disable the timer and deploy compatible code while preparing a later reviewed migration. Removing the S3 backstop or broadening role privileges is not a rollback. A lifecycle apply cannot be undone for versions AWS has already expired.

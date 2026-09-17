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

Database bootstrap creates `mycfc_privacy_retention` as `NOLOGIN`. It has no direct table or sequence privileges and can execute only `privacy_retention_run(uuid,integer)` and `privacy_retention_status()`. The separately invoked root-only credential command provisions the fixed `mycfc_privacy_retention_login` login, with only explicit membership in that capability role and database CONNECT. Put its PostgreSQL URL in `/etc/mycfc/privacy-retention.env`. Do not reuse the web, migration, bootstrap, privacy-executor, or backup identity.

The root-owned file must be mode `0600` and contain only:

```text
PRIVACY_RETENTION_DATABASE_URL=postgres://mycfc_privacy_retention_login:<secret>@postgres:5432/<database>?sslmode=disable
PRIVACY_RETENTION_WORKER_REF=<stable-random-uuid>
PRIVACY_RETENTION_BATCH_LIMIT=500
```

The command refuses a missing gate, credential URL, non-UUID worker reference, or batch outside 1–10,000. It uses `SET LOCAL ROLE mycfc_privacy_retention`, verifies the effective role, runs one transaction, and emits counts and ages only. It never emits a database URL, object key, user ID, request ID, consent ID, or audit content.

## Explicit credential operations

The existing `/app/privacy-retention` binary adds exactly three operator modes: `provision`, `rotate`, and `revoke`. Running without arguments retains the existing bounded maintenance behavior. The modes require effective UID 0; use a separately approved root-only one-shot container from the selected immutable release. They do not enable the retention timer or run maintenance. No production command has been run by this source change; the later GitHub host state machine owns execution.

Required inputs are:

- `PRIVACY_RETENTION_EXPECTED_DATABASE`: exact database name.
- `PRIVACY_RETENTION_ADMIN_DATABASE_URL_FILE`: absolute path to a root:root mode-0600 regular file containing the bootstrap administrator URL. The live database name must match and the connection must be a PostgreSQL superuser.
- `PRIVACY_RETENTION_LOGIN_DATABASE_URL_FILE`: absolute path to a separate root:root mode-0600 regular file containing the new fixed-login URL, with the same endpoint, database and TLS settings. Provision/rotation require a separately generated password of 32–1,024 bytes. Revocation neither reads nor requires this file.

Only a single PostgreSQL URL and optional final newline are accepted in each file. Symbolic links, nonregular files, unsafe ownership/modes, alternate usernames, mismatched endpoints/databases, duplicate or unrecognized query settings, and short/empty passwords fail closed. The only accepted URL query setting is an explicit `sslmode` from `disable`, `require`, `verify-ca`, or `verify-full`. Password bytes never enter command arguments, success output or returned diagnostics. PostgreSQL statement and parameter logging is disabled transaction-locally before a bound password is submitted; the tool does not print driver/SQL errors.

Keep the retention timer/service stopped during every credential change. `provision` requires an absent login and the installed, hardened NOLOGIN capability with its two fixed functions. `rotate` requires the existing login; it rejects login-owned objects, removes prior role memberships and direct application-schema privileges, resets role options, then grants only database CONNECT and non-inherited SET ROLE access to `mycfc_privacy_retention`. This preserves the worker's explicit `SET LOCAL ROLE` boundary. The credential and privilege changes commit atomically. Rotation then terminates old sessions and checks that none remain before reporting success.

`revoke` removes capability membership, clears the password and sets NOLOGIN before terminating existing sessions. It is idempotent if the login is absent; it does not drop owned application objects or database data. If session termination fails after commit, the command reports failure while the committed credential restriction remains in place. Do not restart the timer on any failed outcome. Preserve the new protected file for deliberate retry/reconciliation; never revert to the old password as an automatic compensation.

The only successful output is `privacy_retention_credential_provision_succeeded`, `privacy_retention_credential_rotate_succeeded`, or `privacy_retention_credential_revoke_succeeded`. The executable's failure output remains the fixed `privacy_retention_failed` with nonzero exit. Workflow identity and the reviewed release provide operator audit; no username, database URL, password or file content is emitted. These credentials must remain separate from worker, broker, disable-only, application and backup credentials.

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

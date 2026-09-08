# Privacy execution lifecycle migrations

## Scope

`202609080002_privacy_execution_lifecycle` adds the #244 execution, job, lease, attempt, checkpoint, failure and access-revocation records; the privacy-executor grant; indexed session ownership; attachment guards; and the active-administrator invariant. `202609080003_privacy_worker_api` adds the fixed-search-path worker routines used by the restricted executor database identity.

Both migrations are additive and compatible with the previously deployed application. Existing approved requests remain awaiting execution, existing sessions remain explicitly unindexed, and no execution or destructive work is invented during migration.

## Deployment and rollback

The migration command applies pending schema changes and the privacy privilege boundary in the same transaction. The release flow reapplies hardening afterward as an idempotent check. Production privacy activation and executor credentials remain absent until #248 receives separate approval.

Rollback is forward-only compensation. Do not drop or rewrite execution, attempt, checkpoint, failure, event or access-revocation evidence. If a candidate must be rolled back, keep the additive schema and disable the application privacy gate; introduce any required contract cleanup only in a later reviewed migration after compatibility and retention requirements are satisfied.

## Verification

The database integration suite covers fresh baseline provisioning, a representative #243-to-#244 forward migration, preservation of legacy sessions and approved requests, atomic migration-plus-hardening, inactive-subject attachment guards, and effective web/executor privileges. The service integration suite covers transaction rollback, concurrency, exact replay and fenced worker recovery.

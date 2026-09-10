# Privacy restore infrastructure

Issue #247 defines a restore-independent tombstone ledger so an erased identity cannot silently reappear when an older database backup is restored. The Terraform source is intentionally inert by default and separates infrastructure creation from both runtime write access and offline replay access.

## Independent gates

`privacy_restore_infrastructure_enabled` provisions a dedicated versioned S3 bucket, a customer-managed KMS key, a fixed-code Lambda append broker with a service role, two IAM users without access keys, and two-year compliance-mode Object Lock. This switch alone grants neither user access.

`privacy_restore_ledger_write_enabled` grants the isolated worker only `InvokeFunction` on the exact broker. The worker has no S3, KMS, or Object Lock permission. The broker validates a bounded ciphertext request, conditionally creates the opaque object, and verifies the exact version, checksum, size, encryption and closure retention before returning a receipt; its interface never permits retention changes to an existing version. `privacy_restore_ledger_replay_enabled` separately grants the offline restore reader only version listing, exact-version reads and decryption. Permissions boundaries cap all three identities even if another policy is attached later, and the KMS key and bucket policies restrict append cryptography and writes to the exact broker role and ledger context. No identity permits object deletion, Object Lock bypass, KMS administration, backup access, or unrelated AWS services.

Terraform does not create access keys or place credentials in state. The Lambda service role has no reusable credentials. Caller and replay credential creation, installation and rotation require an approved operational change. The web application must never receive either IAM user identity.

The authorization boundary trusts reviewed Terraform, the broker package and AWS control-plane administration. The broker runtime is the only principal allowed to append; its interface cannot update an existing version, its concurrency is capped at one, and it binds retries to the ciphertext checksum, exact KMS key and non-sensitive locator-key identifier. As with every AWS control-plane boundary, an account administrator able to replace broker code or broaden its permissions remains trusted and must be protected by the deployment review and audit process.

## Retention and irreversibility

Every ledger object is versioned, explicitly encrypted with the dedicated KMS key and protected by compliance-mode Object Lock. The bucket rejects uploads that omit the KMS headers or select another key. Pre-destructive intent records and closure records use separate immutable keys: open intents have no lifecycle expiry, while every closure upload must carry compliance mode and its exact evidence-expiry timestamp as the retain-until date. The bucket policy permits 729-731 remaining days so an upload delayed by less than one day can preserve either a 730- or 731-day calendar interval; the application still verifies the exact database-derived expiry after upload. Closure lifecycle expiry uses 731 days with one-day noncurrent cleanup and expired-marker cleanup as a backstop; the application record remains authoritative for exact calendar-month expiry. The bucket and KMS key also use Terraform destroy protection.

Compliance-mode retention cannot be shortened or bypassed, including by the AWS account root user. Review the exact plan, bucket name, region, key policy and cost before applying the infrastructure gate.

The separately gated PostgreSQL backup lifecycle expires non-current versions after one day and removes expired delete markers. Current daily and monthly backup retention remains 30 and 365 days. S3 lifecycle timing is asynchronous and cannot prove a strict 24-hour maximum by itself, so it is only a backstop. The separately configured `mycfc-postgres-backup-version-cleanup.timer` starts exact-version deletion at 23 hours, runs every 15 minutes, authoritatively re-lists both backup prefixes, and fails if a non-current version or orphan delete marker reaches 24 hours. Its output contains counts and allowlisted event names, never object keys.

## Required rollout sequence

1. Keep all three gates false while source changes are reviewed and deployed.
2. Review a production Terraform plan that enables only the infrastructure gate.
3. Apply it and verify encryption, versioning, public-access blocks, compliance-mode Object Lock and the absence of access policies and keys.
4. Create and install the worker caller credential through the approved worker-only secret path, then separately enable and verify broker invocation. Confirm the caller cannot access S3, KMS, or Object Lock APIs directly.
5. Produce and verify an append-only test tombstone before any destructive privacy execution is allowed.
6. Create the offline replay credential separately, enable replay access only for the restore environment, and run the isolated oldest-retained restore drill.
7. Keep production promotion blocked unless migrations, tombstone replay and absence verification all succeed and produce a current non-identifying attestation.

The backup exact-version cleaner has a separate pair of gates: `postgres_backup_noncurrent_cleanup_enabled` adds its narrowly bounded IAM permission and lifecycle backstop, while host setting `BACKUP_NONCURRENT_CLEANER_ENABLED=true` permits execution. It defaults to inventory-only operation through `BACKUP_NONCURRENT_CLEANER_DRY_RUN=true`; the installer schedules it only when that setting is explicitly false. Review and verify the IAM/lifecycle plan before changing the first gate, record a privacy-safe dry inventory, then disable dry-run and observe the first scheduled cleanup. Runs are forwarded to the durable deployment CloudWatch log group; a dedicated one-datapoint alarm reports a missed 24-hour boundary or verification failure in the next one-minute period.

Disabling access policies stops future ledger writes or reads but cannot undo retained ledger objects, erased records, or deleted object versions. Restore replay is the only approved route for preventing erased identities from returning from a retained backup.

## Hetzner backup posture evidence

Terraform explicitly requests backups for the application server, but configuration alone is not runtime evidence of provider rotation or manual-snapshot expiry. The disabled-by-default `mycfc-hetzner-backup-posture.timer` therefore performs a daily read-only check with a separately protected, project-bound token. It resolves the exact server, confirms the backup window, paginates the complete backup/snapshot image inventory twice, and refuses evidence if the two canonical inventories differ.

The check requires no more than seven automatic backups bound to the server. Every manual snapshot created from that server requires an opaque accountable owner reference, a non-identifying reason code, a provider-matching creation timestamp, and an expiry no later than 30 days after creation. Missing, inconsistent, overlong, or overdue metadata fails the run; the mechanism never deletes or modifies provider resources. Output is limited to counts, oldest ages, and a digest of the canonical scoped inventory. Enabling the timer or deleting an overdue snapshot remains a separate operational approval.

## Verification

`make terraform-test` runs mocked policy and gating tests for both the production worker and Hetzner restore infrastructure. The tests cover inert defaults, gate dependencies, versioning, compliance retention and exact allowlists. They do not replace a reviewed live plan or post-apply evidence.

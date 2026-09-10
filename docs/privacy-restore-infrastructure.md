# Privacy restore infrastructure

Issue #247 defines a restore-independent tombstone ledger so an erased identity cannot silently reappear when an older database backup is restored. The Terraform source is intentionally inert by default and separates infrastructure creation from both runtime write access and offline replay access.

## Independent gates

`privacy_restore_infrastructure_enabled` provisions a dedicated versioned S3 bucket, a customer-managed KMS key, two IAM users without access keys, and two-year compliance-mode Object Lock. This switch alone grants neither user access.

`privacy_restore_ledger_write_enabled` grants the isolated ledger writer only append, exact-version verification, encryption, decryption for read-back verification, and data-key operations under `tombstones/`. `privacy_restore_ledger_replay_enabled` separately grants the offline restore reader only version listing, exact-version reads and decryption. Permissions boundaries cap both identities even if another policy is attached later, and the KMS key policy restricts cryptographic use to the two exact identities and ledger encryption context. Neither policy permits ordinary current-object reads, object deletion, Object Lock bypass, KMS administration, backup access, or unrelated AWS services.

Terraform does not create access keys or place credentials in state. Credential creation, installation and rotation require an approved operational change. The web application must never receive either identity.

## Retention and irreversibility

Every ledger object is versioned, explicitly encrypted with the dedicated KMS key and protected by compliance-mode Object Lock for two years. The bucket rejects uploads that omit the KMS headers or select another key. Pre-destructive intent records and closure records use separate immutable keys: open intents cannot age out, while the closure record starts a fresh two-year protection period from verified closure. Lifecycle expiry is set to 731 days with one-day noncurrent cleanup and expired-marker cleanup, avoiding a second two-year retention period. The bucket and KMS key also use Terraform destroy protection.

Compliance-mode retention cannot be shortened or bypassed, including by the AWS account root user. Review the exact plan, bucket name, region, key policy and cost before applying the infrastructure gate.

The separately gated PostgreSQL backup lifecycle expires non-current versions after one day and removes expired delete markers. Current daily and monthly backup retention remains 30 and 365 days. S3 lifecycle timing is asynchronous and cannot prove a strict 24-hour maximum by itself, so the gate is only a best-effort backstop; an exact-version scheduled cleaner and authoritative relisting evidence remain required. A reviewed production plan and post-apply inspection are still required before changing or claiming the live posture.

## Required rollout sequence

1. Keep all three gates false while source changes are reviewed and deployed.
2. Review a production Terraform plan that enables only the infrastructure gate.
3. Apply it and verify encryption, versioning, public-access blocks, compliance-mode Object Lock and the absence of access policies and keys.
4. Create and install the ledger-writer credential through the approved worker-only secret path, then separately enable and verify write access.
5. Produce and verify an append-only test tombstone before any destructive privacy execution is allowed.
6. Create the offline replay credential separately, enable replay access only for the restore environment, and run the isolated oldest-retained restore drill.
7. Keep production promotion blocked unless migrations, tombstone replay and absence verification all succeed and produce a current non-identifying attestation.

Disabling access policies stops future ledger writes or reads but cannot undo retained ledger objects, erased records, or deleted object versions. Restore replay is the only approved route for preventing erased identities from returning from a retained backup.

## Verification

`make terraform-test` runs mocked policy and gating tests for both the production worker and Hetzner restore infrastructure. The tests cover inert defaults, gate dependencies, versioning, compliance retention and exact allowlists. They do not replace a reviewed live plan or post-apply evidence.

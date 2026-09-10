# Disabled privacy-worker infrastructure

Issue #246 adds Terraform source for an isolated AWS identity that a future privacy worker can use for version-aware application-media erasure. Source delivery does not provision, credential, install, start, schedule, alarm, or activate that worker, and it does not perform a purge or live erasure.

## Separate gates

All three production variables default to `false`:

1. `privacy_worker_infrastructure_enabled` provisions an IAM user with a permissions boundary, an empty Secrets Manager container, and a dedicated CloudWatch log group with 90-day retention. It creates no IAM access key and no secret version or value.
2. `privacy_worker_s3_deletion_enabled` adds only `s3:ListBucketVersions` and `s3:DeleteObjectVersion` for `profiles/*`, `repairs/*`, and `equipment/*` in the application media bucket. It requires the infrastructure gate.
3. `privacy_worker_metadata_rewrite_enabled` adds `s3:GetObjectVersion` for repair/equipment sources and `s3:PutObject` for the closed `repairs/retained/*` and `equipment/retained/*` destinations. It requires both earlier gates.

These Terraform gates grant capability only. Creating and installing a worker credential, populating its secret, installing or starting a service/timer, registering an execution capability, enabling the privacy workflow, running an inventory/purge, and performing an erasure each require their own reviewed operational change and human approval.

The identity has no ordinary `s3:DeleteObject`, unversioned `s3:GetObject`, wildcard S3, backup-bucket, application-secret, ECR, Route 53, SSM, log-read, or Object Lock bypass permission. Existing `host_runtime` permissions are not changed.

## Apply and verification boundary

Before proposing the first infrastructure apply, obtain a reviewed production plan and confirm the live bucket's versioning, Object Lock, MFA-delete, replication, exact key prefixes, and legacy-object inventory. Do not add a secret value or access key to the Terraform state. Provisioning the inert infrastructure and enabling either S3 permission set are separate apply approvals.

Run the source checks without contacting production:

```sh
make terraform-fmt
make terraform-validate
make terraform-test
make terraform-lint
```

The mocked Terraform contract tests cover inert defaults, dependency gates, the 90-day log retention, declared prefixes/actions, and prohibited-action exclusions. A reviewed production plan remains required to verify the rendered effective policies before any apply.

## Rollback

Capability rollback sets `privacy_worker_metadata_rewrite_enabled = false` and `privacy_worker_s3_deletion_enabled = false`, reviews the plan, and applies it. Preserve the IAM user, empty secret container, log group, queued jobs, and evidence while diagnosing; the user remains unusable without a separately created credential. Do not set the infrastructure gate back to `false` as an incident shortcut because the secret and log group intentionally have destroy protection.

Disabling capability cannot restore versions already deleted or identifiers already scrubbed. Those actions are intentionally irreversible; backup expiry, tombstone replay, and any restore promotion remain under #247.

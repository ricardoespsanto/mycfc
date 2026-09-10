# Privacy worker activation and operations

Status: source-complete and disabled by default. This source installs no credential, enables no Terraform gate, records no production evidence, starts no worker, and performs no deletion. Provisioning, evidence recording, dual approval, application enablement, worker enablement, and any irreversible purge are separate human-authorized production changes.

## Runtime boundary

`privacy-worker` is a single non-root, read-only Compose service managed by `mycfc-privacy-worker.service`. It uses a generated per-process worker reference, claims one category job at a time, refreshes the lease during a bounded operation, recovers at most the configured number of succeeded-but-unclosed executions per cycle, and stops cleanly on `SIGTERM`. Polling, lease, heartbeat, status, attempts, and completion batch values have hard minimums and maximums; invalid or partial configuration prevents startup.

The service checks `privacy_worker_activation_ready()` before startup, before every claim, immediately beside every checkpoint/object/provider operation, before each destructive S3 version-deletion batch, and again before closure and completion mutations. That executor-only database function recomputes the current activation, exact four-kind unexpired evidence set, fulfilment-ready policy, and distinct executor-proposer/administrator-approver requirement against the database clock. A revoked, expired, incomplete, or superseded activation stops the running service. The claim procedure also applies its own database-clock hard gate, so process timing cannot bypass a revocation. There is no environment boolean that can replace database evidence or dual approval.

The runtime order is activation check, bounded stale-upload cleanup, bounded completion recovery, one fenced category claim, ordered checkpoint execution, job completion, restore-closure export, and completion-manifest creation. Completion recovery is idempotent. `PROVIDER_RECIPIENT_NOTIFY` remains terminally unavailable because the production provider registry is deliberately closed and empty; do not add a provider flag, credential, or adapter without factual #109 evidence and its own review.

The PostgreSQL login must be exactly `mycfc_privacy_executor`. It receives only the reviewed security-definer worker routines and narrow evidence reads. The web application never receives its password, and the executor cannot call the application activation procedures. `db-bootstrap` and `migrate` accept `PRIVACY_EXECUTOR_DB_USER` and `PRIVACY_EXECUTOR_DB_PASSWORD` only to create or re-harden the `NOLOGIN`/login boundary during an approved release.

## AWS capability gates

All production Terraform gates default to `false`:

1. `privacy_worker_infrastructure_enabled` creates the bounded IAM user, empty protected secret container, and 90-day write-only CloudWatch log group. Terraform creates no access key and no secret value.
2. `privacy_worker_s3_deletion_enabled` adds only `s3:ListBucketVersions` and `s3:DeleteObjectVersion` for `profiles/*`, `repairs/*`, and `equipment/*` in the media bucket.
3. `privacy_worker_metadata_rewrite_enabled` adds only the reviewed repair/equipment source reads and retained-prefix writes. The current runtime does not exercise this future capability, so leave it false.
4. `privacy_worker_ledger_broker_invoke_enabled` adds only `lambda:InvokeFunction` for the exact unqualified restore-ledger broker ARN in `privacy_worker_ledger_broker_function_arn`.
5. `privacy_worker_monitoring_enabled` creates the terminal/aged-work and missing-heartbeat alarms.

The worker has no wildcard service action, ordinary `s3:DeleteObject`, backup-bucket access, Object Lock bypass, application-secret access, log-read permission, or infrastructure mutation permission. Credentials must be created outside Terraform and rotated without writing them to Terraform state.

## Protected worker configuration

`/etc/mycfc/privacy-worker.env` must be root-owned mode `0600`. `/etc/mycfc/privacy-worker/keys` must be `root:65532` mode `0750`; every file in it must be `root:65532` mode `0440`. The env file contains references, key identifiers, and bounded runtime settings, not key bytes. A production template is:

```dotenv
PRIVACY_EXECUTOR_DATABASE_URL=postgres://mycfc_privacy_executor:REDACTED@postgres:5432/mycfc?sslmode=disable
AWS_REGION=eu-west-1
AWS_SHARED_CREDENTIALS_FILE=/run/secrets/mycfc/aws-credentials
S3_BUCKET_NAME=REVIEWED_MEDIA_BUCKET
S3_FORCE_PATH_STYLE=false
PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME=REVIEWED_EXACT_FUNCTION_NAME
PRIVACY_WORKER_LOG_GROUP=/mycfc/production/privacy-worker
PRIVACY_COMPLETION_DETAIL_BASE_URL=https://mycfcoimbra.com/privacidade/conclusao
PRIVACY_WORKER_POLL_INTERVAL=5s
PRIVACY_WORKER_LEASE_DURATION=2m
PRIVACY_WORKER_HEARTBEAT_INTERVAL=20s
PRIVACY_WORKER_STATUS_INTERVAL=5m
PRIVACY_WORKER_MAX_ATTEMPTS=5
PRIVACY_WORKER_COMPLETION_BATCH=10
PRIVACY_UPLOAD_EVIDENCE_KEY_ID=REVIEWED_KEY_ID
PRIVACY_OBJECT_EVIDENCE_KEY_ID=REVIEWED_KEY_ID
PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID=REVIEWED_KEY_ID
PRIVACY_TOMBSTONE_LOCATOR_KEY_ID=REVIEWED_KEY_ID
PRIVACY_PROVIDER_EVIDENCE_KEY_ID=REVIEWED_KEY_ID
```

The required mounted files are `aws-credentials`, `upload-private.key`, `upload-evidence.key`, `object-target-private.key`, `object-evidence.key`, `tombstone-public.key`, `tombstone-locator.key`, `provider-target-private.key`, `provider-evidence.key`, `provider-credential-digest-keys.json`, and `completion-delivery.key`. Key files are standard-base64 encoded. The completion-delivery file must contain the same 32-byte source key as the web application’s `EMAIL_VERIFICATION_HMAC_KEY_B64`; it is mounted separately so the worker does not receive the application secret set. The UI and worker apply purpose separation internally. Provider files remain required so a future registry cannot be enabled by partial configuration; the current digest keyring is the exact empty JSON object `{}`.

## Evidence-bound activation

`privacy-activation.sh` records evidence; it does not approve or enable the workflow. It runs a root-only, read-only, capability-dropped one-shot container using `/etc/mycfc/privacy-activation.env` (root mode `0600`) and `/etc/mycfc/privacy-activation/evidence` (`root:root`, mode `0700`). Evidence files are `restore-attestation.json`, `restore-attestation.key`, `infrastructure.json`, `provider-registry.json`, `schema-inventory.json`, and `artifact-public.key`, all `root:root` mode `0600`. The CLI rejects symlinks, non-regular secret inputs, non-owner files, and any secret mode other than `0600`.

The generic evidence signer’s Ed25519 private key stays outside the production verifier. `artifact-public.key` is its standard-base64 public key. Infrastructure, provider, and schema artifacts include an exact contract, `SUCCEEDED`, RFC3339 observation time, policy version, executor and plan-schema versions, immutable `sha256:` deployed-image digest, an exact `s3://…?versionId=…` evidence reference, the referenced object’s SHA-256, signing-key ID, and base64 Ed25519 signature over canonical JSON without `signature_ed25519`. Mutable URLs and unversioned S3 objects are rejected.

Infrastructure evidence additionally binds both current Terraform state serials, SHA-256 digests of both pulled states and reviewed plans, and every required capability gate. Provider evidence binds the immutable provider-registry digest; activation eligibility requires a later reviewed `READY`, nonempty adapter registry, which this release cannot produce. Schema evidence binds the restore schema digest and current baseline migration. These facts must be produced from the corresponding state, plan, registry, image, and schema observations by separately reviewed signing tooling; operators must not hand-author an artifact or treat a signature as proof of an unobserved fact. All three artifacts must be no older than 90 days and match the immutable image digest derived from the active release, the migration digest compiled into that release, the selected policy, the compiled executor/plan-schema versions, and the configured signing trust root. Terraform state serials remain authenticated fields in the signed infrastructure artifact rather than separately asserted environment values.

`cmd/privacy-activation-artifact` is the offline producer. Its `*-observe` commands derive a canonical observation from Terraform 1.15.8 state and complete no-change/no-drift plan JSON, the exact ordered database migration inventory plus checked-in migration directory, or the strict provider registry source. Upload that canonical observation to a versioned S3 evidence bucket using the separately reviewed KMS key, retain the `head-object --checksum-mode ENABLED` response, then run the matching `*-sign` command with the same sources. Signing succeeds only when the downloaded observation is byte-for-byte equal to a fresh derivation, its SHA-256 matches `ChecksumSHA256`, and the head response has the exact `VersionId`, content length, `aws:kms`, and expected KMS key ARN. The Ed25519 private key file must have no group or other permissions and is never installed in the app, worker, activation container, or production secret directory. Terraform source files may contain sensitive values: the canonical observation contains only serials, booleans, the selected release digest, and source digests; never upload raw state or plan JSON as activation evidence.

The currently shipped provider registry source is exactly `{"contract":"mycfc/privacy-provider-registry-source/v1","registry_state":"EMPTY","providers":[]}`. The producer signs that observation only as `NOT_READY`; the activation recorder rejects it. A `READY`, nonempty provider artifact requires a later reviewed provider adapter/registry contract and cannot be produced by this release. This preserves the worker’s disabled state even if every infrastructure gate has been planned.

```dotenv
PRIVACY_ACTIVATION_EVIDENCE_ENABLED=true
PRIVACY_ACTIVATION_BROKER_DATABASE_URL=postgres://mycfc_privacy_activation_broker@postgres:5432/mycfc?sslmode=disable
PRIVACY_ACTIVATION_ACTOR_REF=REVIEWED_ACTIVE_ADMIN_UUID
PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE=/run/privacy-activation/restore-attestation.key
PRIVACY_ACTIVATION_ARTIFACT_PUBLIC_KEY_FILE=/run/privacy-activation/artifact-public.key
PRIVACY_ACTIVATION_RESTORE_ATTESTATION_FILE=/run/privacy-activation/restore-attestation.json
PRIVACY_ACTIVATION_INFRASTRUCTURE_FILE=/run/privacy-activation/infrastructure.json
PRIVACY_ACTIVATION_PROVIDER_FILE=/run/privacy-activation/provider-registry.json
PRIVACY_ACTIVATION_SCHEMA_FILE=/run/privacy-activation/schema-inventory.json
PRIVACY_ACTIVATION_POLICY_VERSION=REVIEWED_POLICY_VERSION
PRIVACY_ACTIVATION_ARTIFACT_SIGNING_KEY_ID=REVIEWED_KEY_ID
```

For `prepare-approvals`, capture the single stdout JSON document into a new root-owned mode-0600 host file under an owner-only umask; the evidence mount remains deliberately read-only. Each offline signer sets `PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE`, `PRIVACY_ACTIVATION_APPROVAL_ROLE`, `PRIVACY_ACTIVATION_APPROVAL_ACTOR_REF`, `PRIVACY_ACTIVATION_APPROVAL_SIGNING_KEY_ID`, `PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE`, and a new `PRIVACY_ACTIVATION_APPROVAL_OUTPUT`. For `activate`, install the returned canonical envelopes and configure `PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE`, `PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE`, `PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE`, their two public-key files, and exact allowlisted signing-key IDs. The prepare output contains only proposal/evidence/release digests and UUIDs; it contains no database credential or private signing material.

The runner derives the current image digest from the active immutable `MYCFC_IMAGE` in `/etc/mycfc/mycfc.env`; it cannot be supplied as a standalone approval shortcut. The activation binary derives the schema migration digest from its embedded migration inventory and ignores any externally asserted schema digest. It passes those trusted current-release values to the shared strict verifier, validates the complete four-item set before recording the first row, and repeats verification at each broker write boundary. Identical evidence recording is idempotent, so an interrupted four-item write can be safely resumed.

Activation is never performed in the web application. After evidence recording, run `privacy-activation prepare-approvals` through the root-only broker environment and distribute the mode-0600 material to two independent signers. Each signer runs `privacy-activation sign-approval` outside the app/worker host with its own allowlisted Ed25519 private key, role (`EXECUTOR` or `ADMINISTRATOR`), and actor UUID. The exact canonical envelope binds the proposal UUID, ordered evidence IDs and digest, activation digest, current policy/executor/plan/image/schema tuple, role, actor, key ID, a random nonce, and a maximum 15-minute lifetime. Run `privacy-activation activate` only after receiving both envelopes and configuring their independent public trust roots. The broker re-verifies both signatures before one atomic SQL call; PostgreSQL rebinds every envelope field, persists the exact signed bytes, rejects reused nonces, and only then records the proposal/approval and clears the kill switch. Broker credentials and approval private keys are root/operator secrets and must never be mounted into the web app or worker.

## Staged rollout and checks

Run source-only checks first:

```sh
make test-deployment
make terraform-fmt
make terraform-validate
make terraform-test
make terraform-lint
go test ./cmd/privacy-worker ./cmd/privacy-activation ./cmd/server
```

For a later separately approved production rollout: review and apply the inert worker identity; create its credential outside Terraform; review and apply exact S3, broker, and monitoring capabilities; install protected files; complete a current signed restore drill and signed infrastructure/provider/schema observations; record the four evidence items; obtain the distinct proposal and approval; then set `PRIVACY_REQUESTS_ENABLED=true`, `PRIVACY_COMPLETION_ENABLED=true`, and `PRIVACY_WORKER_ENABLED=true` and rerun the installer. Before activation, run `privacy-worker.sh readiness`. After activation, verify `systemctl status mycfc-privacy-worker`, the aggregate CloudWatch stream, both alarms, and a non-production synthetic execution. Never use a live subject as a smoke test. Later releases stop the enabled worker before migration and restart it only after the new application passes post-switch checks; the release log records `privacy_worker_stop`, `privacy_worker_readiness`, `privacy_worker_restart`, and `privacy_worker_verify`, and rollback restores the old environment before restarting the previous worker image.

Logs contain only fixed lifecycle/operation codes and aggregate counts: start, claim count, each allowlisted step start/success, retry/terminal outcome, completion outcome, heartbeat status counts, aged-nonterminal breach, activation loss, stop, and remote log-delivery failure. They never include execution/request/user IDs, recipients, tokens, object keys, provider identifiers or responses, digests, SQL text/payloads, or raw errors. A terminal job, any nonterminal job aged beyond 15 minutes, or a completed execution that cannot be sealed alarms after the first matching one-minute period. Missing two consecutive five-minute heartbeats alarms independently.

## Rollback

Stop `mycfc-privacy-worker.service` first and set both `PRIVACY_WORKER_ENABLED=false` and `PRIVACY_COMPLETION_ENABLED=false`. Revoke the database activation through the approved application procedure. Then review and apply Terraform with broker invocation, S3 deletion, rewrite, and monitoring gates false. Preserve the IAM identity, empty secret container, log group, database evidence, queued jobs, and failed leases while diagnosing; protected Terraform resources intentionally have destroy protection.

Rollback is compensating, not restorative. Disabling capability cannot restore deleted versions, scrubbed identifiers, sent notices, or written Object Lock evidence. Recovery must use the separately governed authenticated backup and tombstone-replay procedure. Do not delete evidence or attempt a reverse migration.

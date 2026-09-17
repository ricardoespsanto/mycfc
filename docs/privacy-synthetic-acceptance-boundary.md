# Synthetic privacy acceptance boundary

The `privacy-acceptance` runner exercises the real service, executor, authenticated
closure-ledger export and completion finalizer using the isolated fixture below.
It signs evidence only after the protected absence observer succeeds and fixture
cleanup completes. Its canary evidence records database conditions; CloudWatch
alarm transitions require the separate operations observer.

The migration creates no login or password. It adds an immutable synthetic
registry, generated synthetic subject/reviewer/executor identities, scoped worker
claims and a notification simulation boundary. Existing unmarked requests retain
the ordinary queue path. Ordinary claims, pending-completion discovery and worker
alarm counts exclude synthetic requests. Synthetic notification rows are
cancelled before becoming visible and cannot be requeued or retargeted. No
external delivery timestamp is fabricated.

## Credential operations

The root-only `privacy-acceptance` executable exposes credential operations
`provision`, `rotate`, and `revoke`. No operation accepts a person,
member, request or fixture identifier. The login name is fixed to
`mycfc_privacy_acceptance`; it has schema usage and execution rights only for the
three protected fixture APIs below. It must be separate from app, worker and
migration credentials.

All operations require `PRIVACY_ACCEPTANCE_EXPECTED_DATABASE` and
`PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE`. Provision and rotate also require
`PRIVACY_ACCEPTANCE_DATABASE_URL_FILE`, containing the target operator DSN and a
password of at least 32 bytes. The two DSNs must name the same host, port and
expected database. The command checks the actual connected database before any
role mutation. Credential files must be root-owned, root-group, regular files
with mode 0600 inside operator-controlled directories; the command refuses
symlinks, oversized files, directories and FIFOs. It does not generate, write or
print credential values.

Provision and rotate strip existing object privileges and role memberships;
object-owner roles are rejected. Both replace the password and terminate existing
sessions. Revoke removes privileges, clears the password, disables login and
terminates sessions. Provisioning also revokes PUBLIC temporary-table privileges and public-schema usage,
as the existing dedicated operator provisioning does; app and worker roles must
already have their explicit hardened schema grants.

## Harness contracts

- `privacy_protected.acceptance_create(password_hash, image_digest, schema_digest)`
  requires the fixed session login and current authenticated activation bindings.
  It generates every identity and a 256-bit capability in PostgreSQL. The marker
  is HMAC-SHA256 over the versioned fixture/identity/image/schema binding. Only the
  capability hash is stored. No existing identity can be adopted.
- The service request connection sets `mycfc.acceptance_proof` to the capability's
  hex encoding. A trigger permits exactly one generated-subject request and
  permanently binds it to its fixture. Retargeting either an existing request or
  a fixture request is rejected. Synthetic users cannot obtain sessions,
  verification tokens or password-reset tokens.
- `ExecutionWorker.AcceptanceProof` invokes
  `privacy_acceptance_claim(lease_milliseconds, worker_ref, proof)`. The database
  verifies the exact fixture/request and retains ordinary lease fencing,
  dependencies and `SKIP LOCKED`. The capability expires after one hour and is
  invalidated by finish. An empty proof retains ordinary-worker behavior.
- `privacy_protected.acceptance_observe(worker_ref, proof)` requires actual
  completed request/execution, erased identity, no subject sessions/profile/activity
  connection, zero object/provider targets, a simulated completion notice,
  cancelled fixture outbox rows and an authenticated closure-v4 receipt. It
  returns only digests and counts. It rejects unfinished work.
- `privacy_protected.acceptance_finish(worker_ref, proof)` is idempotent and works
  after capability expiry for cleanup. It disables only the generated identities,
  revokes their reviewer/executor grants and prevents further claims.

## Runner and canary commands

Each invocation accepts exactly one command, with no member, person, request,
execution or fixture identifier. Each creates a fresh database-generated fixture.

| Command | Observed conditions before completed acceptance |
| --- | --- |
| `run` | Request, review, every production checkpoint, authenticated ledger closure, completion notice simulation and protected absence observation |
| `canary-retry` | Actual retry scheduling, followed by completed acceptance |
| `canary-failure` | Actual terminal failure; returns `CANARY_VERIFIED`, without a completion or absence claim |
| `canary-aged` | A pending fixture job ages at least 15 minutes by database time, followed by completed acceptance |
| `canary-heartbeat` | An expired one-second lease rejects heartbeat, a new lease epoch reclaims the same job, then completed acceptance |
| `canary-recovery` | Missed heartbeat and lease recovery, then retry scheduling and completed acceptance |

The runner accepts no alternate clock, age adjustment or global queue operation.
Unexpected object targets are refused without an S3 call. The factual empty
provider registry is used unchanged. Ledger intent and closure writes use the
existing Lambda broker and configured production tombstone protection keys.
Notification records remain cancelled by the database trigger; no email is sent.
The review record belongs to generated reviewers and explicitly labels the
verification and approval as synthetic simulation.

Required binding values are `PRIVACY_ACCEPTANCE_EXPECTED_DATABASE`,
`PRIVACY_ACCEPTANCE_APP_ROLE`, `PRIVACY_ACCEPTANCE_IMAGE_DIGEST`, and
`PRIVACY_ACCEPTANCE_SCHEMA_DIGEST`. The schema digest must equal the running
binary's embedded migration digest. The active approval is pinned before and
after fixture creation and checked throughout execution. Any activation change,
kill switch, expired capability, lost lease or incomplete evidence stops the run.

Required root-owned 0600 regular DSN files:

- `PRIVACY_ACCEPTANCE_DATABASE_URL_FILE`: fixed acceptance login.
- `PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE`: the configured app login.
- `PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE`: fixed executor login.

These connections must match the expected database, host and port. The runtime
rejects powerful database roles, an app login able to insert activation approvals,
and an executor login able to update people directly. Run modes do not use a DBA
credential.

Signing uses `PRIVACY_ACCEPTANCE_SIGNING_KEY_ID` and
`PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE` (base64 Ed25519 seed or private key).
Tombstone configuration uses `PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE`,
`PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE`, `PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID`,
`PRIVACY_TOMBSTONE_LOCATOR_KEY_ID`, `PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME`,
and `AWS_REGION`. Key files use the same owner/mode/no-follow validation.

The deadline is 45 minutes. Cancellation attempts fixture cleanup using a separate
30-second context. Cleanup disables only generated identities and revokes their
capability and grants. Failed cleanup prevents signed output. Exit code zero
requires a verified outcome, cleanup and successful output; errors produce only
`privacy_acceptance_failed` on stderr.

## Evidence and monitoring contract

Stdout first contains fixed observed-event lines, then one JSON envelope. The
host wrapper must capture this output to a protected file and verify it against
a separately trusted signing public key. The envelope does not provide a public
key to trust.

Event lines have the form
`event=privacy_acceptance_canary_<condition>_observed count=1`.
Their exact ordered conditions are: `run`: none; retry: `retry,recovery`;
failure: `failure`; aged: `aged,recovery`; heartbeat:
`heartbeat_missing,recovery`; recovery:
`heartbeat_missing,retry,recovery`. Recovery is emitted only after actual
execution, ledger closure, completion and protected absence verification.

The envelope contract is `mycfc/privacy-synthetic-acceptance/v1`, with fields
`contract`, `key_id`, `payload`, `payload_sha256`, and `signature`.
The signature is base64 Ed25519 over the contract, NUL, key ID, NUL, and exact
compact payload JSON bytes. The SHA-256 is of those payload bytes. The payload
contains only release/policy/fixture/manifest digests, mode, outcome, UTC timestamps,
checkpoint/notice counts and allowlisted conditions. It contains no person,
request, worker, email, credential, raw error or capability value.

A completed run has `outcome=COMPLETED` and a manifest digest. Terminal-failure
canaries have `outcome=CANARY_VERIFIED` and no manifest. A canary signal can exist
without a final signed result if a later operation fails; that signal alone must
never satisfy the acceptance gate. Observing these events is not evidence that
CloudWatch alarms reached ALARM or OK.

## Local verification

`TestSyntheticAcceptanceRealRolesAndHandlers` creates a separate PostgreSQL
database, installs the baseline as the real migration login, hardens app and
executor permissions, and exercises all six commands with those actual roles.
Only the external ledger transport is captured by a test double; request,
checkpoint, receipt, completion and absence contracts run in PostgreSQL. Every
mode checks ordinary-subject preservation, notification isolation, fixture
cleanup, ordered condition events and signability of the observed evidence.
For the aged case, test-only DBA setup adjusts timestamps exclusively for one
newly registered synthetic execution. The production command still waits for
actual database age and has no timestamp adjustment interface. These tests do
not establish live Lambda delivery or CloudWatch alarm transitions.

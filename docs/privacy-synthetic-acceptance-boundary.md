# Synthetic privacy acceptance boundary

This additive boundary is a prerequisite for #274 production acceptance. It does
not declare a completed acceptance run. The full harness must use the real
executor handlers, completion finalizer and authenticated closure-ledger export;
it must emit signed evidence only after `acceptance_observe` returns one row.
The current mandatory executor support gaps must be resolved before that harness
can report success.

The migration creates no login or password. It adds an immutable synthetic
registry, generated synthetic subject/reviewer/executor identities, scoped worker
claims and a notification simulation boundary. Existing unmarked requests retain
the ordinary queue path. Ordinary claims, pending-completion discovery and worker
alarm counts exclude synthetic requests. Synthetic notification rows are
cancelled before becoming visible and cannot be requeued or retargeted. No
external delivery timestamp is fabricated.

## Credential operations

The root-only `privacy-acceptance` executable currently exposes exactly these
operations: `provision`, `rotate`, and `revoke`. No operation accepts a person,
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
  returns only digests and counts. It rejects unfinished work. This slice does
  not sign external evidence or perform an acceptance run.
- `privacy_protected.acceptance_finish(worker_ref, proof)` is idempotent and works
  after capability expiry for cleanup. It disables only the generated identities,
  revokes their reviewer/executor grants and prevents further claims.

Alarm canaries and the full acceptance runner are intentionally pending the real
executor implementation. They must remain fixture-scoped, distinguish simulation
from delivery, and must not turn ordinary worker alarms into evidence of a live
canary observation.

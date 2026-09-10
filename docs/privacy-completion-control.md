# Privacy completion control

The #248 completion control remains inactive until a reviewed release enables
the privacy worker and the evidence-bound activation protocol succeeds. No
migration seeds evidence, proposes activation, approves activation, or starts a
worker.

## Completion boundary

Starting an execution copies the already encrypted, generic processing-notice
delivery target into `privacy_protected`. Cleartext contact data is not copied
into an execution row, manifest, event, log, or completion-link record.

After every category job and checkpoint succeeds, the tombstone broker writes
and verifies the v2 closure record. The completion worker then invokes one
database finalizer. Under row locks, that finalizer compares the immutable plan
with every category and operation position, requires result digests for every
checkpoint, recounts object and provider targets against their capture sets,
requires one structured evidence record per target, rejects outstanding
provider credentials and leases, and requires both v2 tombstone receipts. It
stores a canonical metadata-only manifest and digest, moves the request to
`COMPLETED`, appends the event, activates one completion link and enqueues the
generic completion notice in the same transaction. SMTP failure does not undo
completion.

The link contains only 256 bits of random entropy. Only its SHA-256 digest is
stored as a searchable token. The raw token exists in the encrypted outbox
payload, is usable once, and expires 24 hours after activation. Expired, used,
unknown and malformed tokens have the same result. The returned detail is an
opaque completion summary and never includes identity, email, object keys,
provider identifiers, category decisions or diagnostic text.

Mail scanners must not consume the capability. A GET may call the
non-consuming `ValidateCompletionLink` only to render a generic confirmation;
the bounded summary is consumed only after an explicit requester POST. The
handler binds the token to a short-lived encrypted, HttpOnly, SameSite cookie,
so the raw token is not copied into form markup. No account session is needed.

The executor-only pending-completion query deliberately finds succeeded work
before closure evidence exists. Runtime ordering is: list a bounded batch,
write and verify the closure-v2 tombstone, then invoke the atomic finalizer.
The finalizer remains unavailable until the closure receipt exists.
An executor-only status routine publishes only pending, leased, retry-wait,
terminal and over-15-minute nonterminal aggregate counts for heartbeat/alarm
logs; it exposes no execution, subject or target identifiers.

## Terminal requeue

A terminal job cannot be directly reset. An active privacy executor proposes a
requeue bound to the exact terminal failure, execution version, lease epoch and
attempt count. A distinct active administrator must approve the same digest.
Approval adds exactly one manual attempt; it does not reset attempt history or
reuse an earlier failure. A changed execution, later failure, reused proposal,
self-approval or eleventh manual attempt fails closed.

Authorization-checked operator snapshots expose only allowlisted category,
purpose, status, failure-stage and failure-code values plus the pending digest;
they exclude principals, diagnostics, targets and provider details.

## Activation evidence

Activation is two-person and digest-bound. An active executor proposes an
adopted policy against exactly one current item of each kind:

- `RESTORE` uses `mycfc/privacy-restore-drill-attestation/v1`, the authenticated
  restore/promotion attestation contract.
- `INFRASTRUCTURE` uses `mycfc/privacy-infrastructure-posture/v1`.
- `PROVIDER` uses `mycfc/privacy-provider-registry/v1`.
- `SCHEMA` uses `mycfc/schema-migration-inventory/v1`.

Each record carries the SHA-256 digest of the reviewed artifact, an opaque
reference code, its observation time and a fixed 90-day expiry. A distinct
active administrator approves the exact proposal digest while all four records
are still current. Only the owner-controlled approval function can set both
activation flags and bind the singleton to its approval. Direct enablement is
rejected. Deactivation remains a one-way safe operator action.
The forward migration disables and audits any legacy unapproved activation;
it never carries the old caller-provided readiness shortcut into 011.

The restore artifact is the exact HMAC-authenticated drill document and its
container image field must be `sha256:` followed by 64 lowercase hexadecimal
characters. The other three artifact documents must contain their exact
contract, `SUCCEEDED` result and an RFC3339 `observed_at` matching the recorded
time; the server hashes the whole artifact. Evidence ingestion is a trusted
CLI/runtime operation, never an in-browser secret upload. Both web availability
and the worker's no-argument readiness probe recompute expiry against the
database clock, so an enabled row cannot remain effectively live after any
evidence reaches 90 days.
An executor may bind a new four-item set before expiry as a renewal. The
operator snapshot distinguishes initial proposal from renewal, and replaying
the same evidence set fails on its immutable activation digest.
Recording one already-verified artifact is idempotent on its kind and whole
payload digest, so a trusted CLI can safely resume after a partial network
failure without creating or relabelling evidence.

The infrastructure and provider evidence producers, worker service wiring,
HTTP completion-detail route and operator UI are deliberately outside this
backend slice. They must preserve these contracts and keep production disabled
until independent QA, security review and a release approval are complete.

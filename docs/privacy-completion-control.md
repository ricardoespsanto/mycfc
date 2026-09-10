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
and verifies the current authenticated closure record. Older closure versions
remain readable for recovery but are not activation evidence. The completion worker then invokes one
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
write and verify the current tombstone closure, then invoke the atomic finalizer.
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

Activation is two-person, release-bound and digest-bound. An active executor proposes an
adopted policy against exactly one current item of each kind:

- `RESTORE` uses the current v2 independently authenticated restore attestation,
  restore/promotion attestation contract.
- `INFRASTRUCTURE` uses `mycfc/privacy-infrastructure-posture/v1`.
- `PROVIDER` uses `mycfc/privacy-provider-registry/v1`.
- `SCHEMA` uses `mycfc/schema-migration-inventory/v1`.

The non-restore documents are exact-schema Ed25519 envelopes. They bind the
policy, compiled executor and plan versions, deployed image digest, immutable
versioned S3 reference and object checksum to an allowlisted signing key. The
infrastructure record additionally binds both Terraform state serials plus
state and plan digests and every required worker capability. Provider evidence
must describe a closed, non-empty `READY` registry; an `EMPTY` or `NOT_READY`
registry can never activate the worker. Schema evidence binds the binary's
ordered embedded migration-inventory digest and exact baseline cutoff.

The restore record is an HMAC-authenticated independent observer result. It
binds the same release and schema inventory, the exact candidate replay result,
the current closure contract and authenticated erasure-effective clock, and
cross-checks replay, absence and observer totals. Candidate-produced JSON is
never activation-authoritative.

Each record carries the SHA-256 digest of the authenticated artifact, its
observation time and at most a 90-day expiry. A distinct
active administrator approves the exact proposal digest while all four records
are still current. Only the owner-controlled approval function can set both
activation flags and bind the singleton to its approval. Direct enablement is
rejected. Deactivation remains a one-way safe operator action.
The forward migration disables and audits any legacy unapproved activation;
it never carries the old caller-provided readiness shortcut into 011.

Every release image field must be `sha256:` followed by 64 lowercase hexadecimal
characters, and every artifact must match release values derived from the
running image and embedded migration inventory rather than authoritative
environment assertions. Evidence ingestion is a trusted
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

## Database kill switch

Migration 013 engages a database-resident switch and disables every older
activation. Only approval of a complete current v2 evidence set clears it.
Operator deactivation engages it again; there is no standalone enable path.
The claim boundary and every executor checkpoint, object/provider operation,
tombstone/closure operation and completion operation lock and recheck the
switch plus evidence expiry before entering its reviewed implementation. This
also blocks a direct call made with a stale executor credential. A lease held
when the switch is engaged is not heartbeated, completed or marked successful;
its original expiry makes it safely recoverable after a separately approved
reactivation.

The infrastructure and provider evidence producers, worker service wiring,
HTTP completion-detail route and operator UI are deliberately outside this
backend slice. They must preserve these contracts and keep production disabled
until independent QA, security review and a release approval are complete.

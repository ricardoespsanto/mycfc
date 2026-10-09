# One-time disposable release candidate (local, unreleased)

This implements the owner's explicitly authorised replacement of the **current
no-real-data database**, not a generic migration fallback or a reusable wipe
switch. The earlier [counterexample rehearsal](disposable-database-release-rehearsal.md)
and its original script remain historical evidence; use
`scripts/validate-disposable-candidate.py --release-image` for this implemented candidate.

## Bound contract and installed release path

- Only database `mycfc`, predecessor web principal `mycfc_app`, the exact
  `v1.25.9` 66-marker ledger digest
  `8ad238f2a1e36976bebd8f6950c935c9fa5b72862fe7cb14d2ad1149d37d1a4e`, the five
  reviewed application namespaces, and the old membership uniqueness contract
  are eligible. Unknown/partial/later ledgers, other namespaces and an existing
  candidate role without completion are refused. Arbitrary migrated databases
  are not eligible. The reviewed parent is `e90033d`.
- The embedded baseline SHA-256 is fixed at
  `0846ee526863b67e1e3a3dea1cea52d35fb3e0af58994d5f69b0469bc816e98a`; the final
  77-marker digest is fixed at
  `41eca3f0ca37f6e6ba5279fe1d931ff589de72b2a8586a77e31445e8a8b29ed7`.
  Changing the embedded schema/inventory refuses replacement.
- Docker embeds the canonical signed-tag publisher's **exact version and Git
  SHA**. Bootstrap, migration, hardening and web configuration reject a different
  runtime identity. An unstamped development build has no replacement capability.
  Publication validates the actual image's `disposable-release-contract` output
  before host-visible promotion. No candidate version is selected by this local
  change; release/tag/publication approval remains a separate owner decision.
- The unchanged installed tagged poller's `bootstrap-db` command performs the
  replacement. The candidate resolves `mycfc_app` to the distinct restricted
  `mycfc_disposable_web_20261001` role internally, using the same existing app
  password for both URL and AWS component configuration. **No host upgrade,
  SSH, host environment/secret rewrite, privileged mount or Docker socket is
  required.** The newer source poller's generic contract guard also accepts only
  this exact attested contract; unrelated contract migrations remain blocked.

## Atomicity, authentication recovery and safe downtime

Bootstrap first checks its actual database privilege. This implementation
requires the existing bootstrap **superuser** capability; the ordinary migration
role has no superuser/CREATEDB/CREATEROLE and is refused before destruction.
The configured migration role must already own `public` and `mycfc_meta`, remain
unprivileged and distinct from bootstrap, and configured worker roles must not
be privileged. A mismatch returns a capability/ownership blocker before fencing.
There is no attempt to guess a lesser-capability workaround. Local bootstrap
proof is not a live privilege attestation.

A connection-scoped advisory lock serialises replacement attempts. Following
successful preflight, predecessor NOLOGIN is committed and its sessions are
terminated. Then one transaction captures active adult administrator identity
and original bcrypt hashes into a private temporary table, drops only the five
reviewed application schemas, recreates/bootstrap-configures the roles and
schemas, applies the exact embedded final baseline and ledger, restores those
administrators, hardens ACLs, validates the dated contract, and records completion.
At least one already-verified recoverable adult administrator is required;
other active adult administrators retain their original verification state.
No plaintext password is recovered, printed or invented; no CLI reseed is
needed and no random inaccessible administrator account is created. Application
reset errors omit driver/SQL details that could contain retained credentials.
Credentials incompatible with the existing bootstrap SQL formatter are refused
before replacement rather than silently changing the secret.

The private, bootstrap-owned `mycfc_disposable_release.completion` survives all
application schema changes. It binds database/version/candidate/predecessor/final
inventory and records completion time, permanent reset retirement time, and an
optional real-data lifecycle date. The migration role has only a fixed SECURITY DEFINER boolean read API, not table
write access; the web role has neither schema nor receipt access. Migration and
hardening cannot run before exact completion. Retries of exact completion do
not run destructive DDL or rotate credentials; post-reset writes survive.
Missing, partial or mismatched completion is not an instruction to wipe a final
database. Successful replacement atomically inserts non-null `reset_retired_at`
before any candidate process/public writes. Bootstrap and the fixed migration
read API require that retirement evidence, bounded by completion time. The
receipt rejects retirement updates, deletion and truncation; ordinary web and
migration roles cannot access its writes. An exact completed retry reinforces
the predecessor fence only and never enters destructive DDL, whether
`real_data_started_at` is null or set. There is no reopening operation or reset
bypass. A PostgreSQL superuser can intentionally dismantle database guards;
that trusted offline capability is not a web or migration-role privilege.

A destructive-transaction failure restores the old schemas, credentials and
ledger, **but deliberately keeps the predecessor fenced**. This is a safe
maintenance outage, not seamless rollback. After successful replacement,
tagged EXIT rollback restores old configuration and the old Caddy route; its
old application stays alive but database readiness returns 503 directly and
through the restored proxy. **This is not blanket HTTP maintenance:** the exact
tagged Caddy upstream uses `/health/live`, which independently remains 200;
`GET /login` and `GET /registo` render public forms (200), while supported valid
submissions fail with 500 without authentication/account creation. An existing
authenticated member-directory request also fails 500 without personal data.
The old principal is NOLOGIN, has no surviving sessions or public schema/table
read/write ACLs. No all-route 503 guarantee or liveness/readiness equivalence is
claimed. Retry can start the candidate without another wipe. The installed
poller's digest quarantine still applies: its automatic retry policy is not
changed here; a quarantined exact release needs the existing explicitly approved
recovery/replacement procedure.

## Real-data boundary and finite removal plan

The first attempt relies on the owner's explicit attestation that the current
reviewed predecessor is disposable. No SQL classifier can distinguish test rows
from real personal data, and absence of a lifecycle record is **not** a general
real-data attestation. Do not use this image against a restored predecessor or
another deployment merely because its ledger matches.

Reset retirement is automatic at successful completion, **not a future manual
marking prerequisite**. Normal public signup remains open; a new candidate account cannot
arrive before reset retirement. Optional `real_data_started_at` is bookkeeping,
not a destructive-authorisation switch. Preserve the completion record and
retirement in backups/restore reconciliation; never clear it to recover errors.
Missing retirement evidence refuses bootstrap/migration rather than inferring
permission to reset. No SSH or later manual operator access is needed for safety.

Finite source cleanup remains a separately approved subsequent release through
the existing poller, not a requirement to disable registration:

1. Deliver the **next source release without the destructive implementation**,
   temporary publisher validation step and disposable-contract guard exception.
   Preserve the stable new web-role resolver and old role's NOLOGIN fence; do not
   revert runtime configuration to the predecessor role or call ordinary
   bootstrap with that old role. A later identity-parameter update, if desired,
   needs separate approval but does not require changing the app password.
2. Validate that the next release can bootstrap/migrate non-destructively using
   the new role, retains retirement, and cannot re-enter replacement.
   Never extend the predecessor/digest constants or add a persistent wipe flag.
3. Keep privacy execution retired, its worker kill switch engaged, and guardian
   intake disabled. No activation approval is created by replacement/binding.

Existing object storage is untouched. Resetting database provenance can leave
orphan objects; inventory and exact-prefix/version disposition require separate
owner approval. No object deletion, secret write or live change is included.

## Executed local evidence

`python3 scripts/validate-disposable-candidate.py --release-image` builds the actual
production Dockerfile and exact tagged predecessor
and the real stamped candidate, starts independent PostgreSQL/application/Caddy
processes, and executes unchanged tagged database bootstrap/migrate/harden,
guardian bind/status, upstream write/reload and EXIT rollback functions.
Synthetic credentials and identities only; random-prefix resources are cleaned.
The route test uses standard pinned Caddy (not the production rate-limit module).

Verified: wrong privilege before destruction; runtime identity mismatch; partial
ledger and unrelated namespace refusal; migration-before-completion refusal;
real DDL failure rollback and retry; 66→77 replacement; original administrator
identity/credentials, verification state and successful authenticated admin HTTP
session; real registration and new-account login; byte-identical retained auth
fingerprint; real TCP correct/wrong password and old-role rejection; restricted web
ACLs; retired privacy/guardian gates; pre/post-completion retry data survival;
actual candidate proxy readiness; post-switch tagged route/config restoration
with direct/proxy readiness 503 but independent liveness/public-form GET 200,
valid form POST 500/no writes and old authenticated member-directory 500/no PII;
automatic retirement before public writes, retirement update/delete/truncate
refusal, missing/partial retirement refusal and marked real-data safe retry
with both retained administrator and new-account HTTP authentication afterward;
unknown post-completion ledger rejected by both bootstrap and migrate.

Final production-image rehearsal evidence:
`$TMPDIR/mycfc-disposable-evidence-nit_hkin/results.json` (safety follow-up).
The unchanged tagged candidate-validation health/login/fingerprinted-asset
block passed before switch and again after retry. The regression first failed
on the absent retirement column (`$TMPDIR/disposable-lifecycle-red.log`).
An added old-authenticated-session fixture initially failed because the
predecessor baseline had not run its production hardening command; the final
rehearsal includes actual tagged `harden-db` before old login, then proves the
same old session cannot read the member directory after fallback. Cleanup now
removes fixture anonymous volumes too (`docker rm -fv`, Compose `down --volumes`);
the final rehearsal and integration runs left no new Docker volumes, and their
containers/networks/images were removed. Earlier runs inherited the old
container-only cleanup and may have left unlabelled anonymous PostgreSQL
volumes; no shared/global volume prune was attempted.
Final integration evidence: `$TMPDIR/mycfc-disposable-full-gates-c86794bef1`.
Full serial tagged PostgreSQL/handler/storage/media-cleanup/retention/
guardian-activation integration ran on an independent Lisbon PostgreSQL and
KMS-backed MinIO fixture: 1609 pass events, no failures, one existing final-baseline
fixture test skip (`TestDatedParticipationApplyBaselineFromRecordedExpand`) and
one generated-package no-tests skip. Go tests/vet, actionlint, release tooling,
publisher and deployment control tests passed. These shell control tests use
explicit mocks and are **not remote/live provenance evidence**.

Not exercised: live database identity/privileges/secrets, ECR/GitHub attestation
or publication, real installed-host discovery/quarantine recovery, production
Caddy rate limiting, full browser/axe/Terraform `make verify`, or independent
security review. No push, merge, tag, publication, deployment or live SQL occurred.

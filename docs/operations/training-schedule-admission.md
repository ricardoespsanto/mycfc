# Schedule compatibility admission and runtime-only fallback

This source implements the C operational boundary. C writes exact intervals and
v1 artifacts only; P's nullable migration and partial/v2 activation are separate.
Reader generation 2 describes schedule compatibility; later features may require
a higher monotonic reader floor. It is not a permanent universal compatibility
promise. This document authorizes no production operation.

The installed controller differs from the repository controller. No installed
script was available in the historical evidence directory inspected for this
change. Before any installation, obtain the actual nonsecret controller source
under exact live-read authority, review a bounded patch against its hash, and
preserve its existing restore, guardian and retired-privacy gates. Do not replace
it with `pull-release.sh`. The old repository disposable guard does not establish
an installed production blocker.

## Source release path

The authenticated immutable image exposes `normal-release-contract` with exactly
`contract`, `version`, `candidate`, `database`, `parent_digest`, `final_digest`.
Its contract is `mycfc/normal-release/v1`; compiled identity and final inventory
must match the existing authenticated publication manifest. The normal path is
separate from the legacy disposable path. Its predecessor/baseline/reset pins
and old guard are unchanged. The low authenticated migration command checks
actual parent/final ledger and schema; this static declaration does not do that.

Normal releases require admission before environment changes and before the
already-current shortcut. Admission is refreshed after migration before candidate
startup. A legacy image cannot be selected when the host authority exists.
Neither admission nor a normal metadata declaration enables partial timing.
The signed publication manifest keeps its existing exact fields.

Initial C installation cannot infer admission from an absent file or an absent
control schema. The owner-approved initial sequence must apply the C expansion
through the reviewed low-role migration path, prepare the external checkpoint,
reconcile actual state and compatible images, and then admit/start the C image.
Unattended polling refuses until that preparation is complete. This initial
operation needs its own explicit maintenance/rollback plan; do not retry a reset
helper, clear quarantine, guess timing or weaken file checks to get through it.

## Two host artifacts

`/etc/mycfc/deployment/training-schedule-host.json` is the authoritative root-only
mode0600 host record. It retains external restore generation, reconciliation
evidence digest, reviewed controller source hash and any pending floor CAS.
Runtime containers never receive it. The nonsecret projection is
`/etc/mycfc/training-schedule-admission/admission.json`, root-owned exact0444 in
a root-owned exact0755 directory. Production Compose mounts that **directory**
read-only at `/run/mycfc-admission`, with `create_host_path: false`, and sets
`TRAINING_SCHEDULE_ADMISSION_FILE=/run/mycfc-admission/admission.json`.
Directory mounting makes atomic file replacement visible to existing containers.

Only the operator creates these parent directories under the approved host
operation. Missing paths, symlinks, unsafe ancestors/ownership/modes, oversized
documents, duplicate/unknown fields and malformed numbers fail closed. Writes
use protected exclusive temporary files, file fsync, atomic rename and directory
fsync. Python3 is already a repository installer prerequisite; the helper uses
only its standard library and installs no dependency.

The projection protocol is `mycfc/training-schedule-admission/v1` with exact keys:

```json
{
  "contract":"mycfc/training-schedule-admission/v1",
  "admission":"admitted",
  "restore_generation":1,
  "reconciled_restore_generation":1,
  "database_incarnation":"11111111-1111-4111-8111-111111111111",
  "floor_commit_id":null,
  "floor_manifest_sha256":null,
  "minimum_state_epoch":0,
  "minimum_reader":1,
  "images":[{
    "image_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "capabilities":{
      "protocol":1,"version":"v2.0.9",
      "candidate":"0123456789abcdef0123456789abcdef01234567",
      "reader_generation":2,"writer_generation":1,
      "snapshot_read":[1,2],"schema_read":["schedule-c1","schedule-p1"],
      "write":["INTERVAL","snapshot-v1"]
    }
  }]
}
```

The example identities are placeholders, not approved image evidence. Production
values come from the compile-stamped `precision-capabilities` output of the
already authenticated digest image and its existing signed manifest. The host
controller checks the actual image digest; runtime compares its complete compiled
capabilities to a listed entry, plus current DB incarnation/floor/epoch/schema.
The external generations must match and admission must say `admitted`.

`precision-status` is a separate nonsecret low-privilege database diagnostic:
contract `mycfc/training-schedule-status/v1`, database_incarnation,
schema_contract, state_epoch, required_reader, floor_commit_id,
floor_manifest_sha256, partial_v2_enabled, ever_partial_v2_enabled. It exposes no
migration ledger, protected genesis receipt, URL, credential or person data.
Matching copied status alone can never create host admission.

## Trusted reconciliation, not automatic anti-rewind

Every restore must be externally fenced **before** drain/stop/restore begins.
Under the controller maintenance lock, `precision-admission.py block` increments
the external restore generation and durably publishes `blocked` before returning.
Root then performs only the separately approved maintenance operation. A known
restored copy cannot serve until explicit reconciliation, even if its copied
incarnation, floor and epoch equal the old values. Unknown out-of-band rewinds
cannot be detected from these copied values; this mechanism makes no such claim.

Reconciliation requires a private root0600 proof prepared after the actual
operator checks. Exact keys are `contract` (value
`mycfc/training-schedule-reconciliation/v1`), opaque `approval_reference`,
`restore_generation`, `status_sha256`, `controller_sha256`, `manifest_sha256`
(ordered array), `checked_image_digests` (same order), and `checks` (exact array):

```json
["controller-fenced","pre-c-processes-stopped","restore-reconciled",
 "schema-ledger-verified","guardian-release-binding-preserved",
 "privacy-retired","fallback-runtime-only"]
```

These are trusted operator attestations, not facts inferred by a generator.
Current status hash is SHA256 of sorted-key, compact, ASCII JSON plus one newline;
manifest/controller hashes cover their exact bytes. Release manifests must already
have passed the existing provenance/signature policy; this helper does not claim
to authenticate signatures. Its strict checks bind those exact manifests and
image stamps to the private proof. Check actual active and fallback images and
preserve the reader floor during restore reconciliation. No runtime, UI or
caller-supplied environment boolean can produce this proof or enable admission.

The root CLI actions are `block`, `reconcile`, `check`, `prepare-floor`,
`complete-floor`, `normal-contract`. All state operations accept `--status` for
a protected nonsecret snapshot, and use the existing
`/run/mycfc-pull-release.lock`. Nested controller calls use inherited `--lock-fd 9`,
validated against the same root-owned lock inode. `reconcile` additionally needs
`--evidence`, `--controller` and repeated `--release MANIFEST CAPABILITIES` pairs.
`block --expected-generation 0` is the first blocked checkpoint; later blocks
require the actual current generation. A failed CAS is a refusal, not permission
to initialize over an existing record.

Keep the controller lock across verification, host updates and the short DB CAS.
Drain processes and run readiness/network checks **outside** the exclusive DB
state lock. The helper itself performs no SQL, network, Docker or secret reads.
An operator uses the existing migration-owner capability for the reviewed state
mutations; it does not gain a new activation credential.

`prepare-floor` durably records the proposed incarnation/expected epoch/floor
ID/manifest hash/minimum reader before outputting a nonsecret CAS request. It
does not mutate the DB. Every admitted active/fallback image must meet the new
minimum; reader generations above 2 are supported only when the image declares
them. Both floor actions also require `--controller` matching the reviewed hash.
Complete the exact `commit_floor` CAS through the reviewed offline path,
then call `complete-floor` with fresh status. It requires the exact new floor,
epoch+1, monotonic minimum and a still-closed write gate before updating runtime
projection. Host-ahead/DB-behind crashes block automation for explicit
reconciliation. Never lower or discard the pending floor to recover.

Opening P's write gate is later, separate authorization using exact
incarnation/epoch CAS and the matching committed floor. Disabling new writes
advances epoch but retains the permanent floor. A committed floor ID is distinct
from gate epochs, so ordinary enable/disable does not invalidate the projection.
Deleting current NULL rows or finding no v2 prescriptions does not justify
lowering the floor. Restored-copy cleanup does not revive #318 automation.

## Guarded fallback

The adapter `precision-controller.sh check STATUS MANIFEST CAPABILITIES CONTROLLER`
verifies host authority, projection, current status, actual controller source hash
and admitted immutable image before any process action. A hash mismatch requires
review and explicit reconciliation; do not substitute the new hash automatically.

Its `fallback` action additionally accepts `SLOT BINDER_MANIFEST BINDER_CAPABILITIES`
and requires the enclosing controller lock fd to stay inherited. Both images must
be admitted. On P schema, use P's schema-current narrow guardian binder, targeting
C's immutable image digest, and verify guardian intake remains false. Calling C's
older binder on P would correctly fail its strict schema digest check. This path
does not loosen that check or enable intake.

The adapter restarts only `app-blue` or `app-green` at its pinned fallback digest,
checks actual image and direct current readiness, and returns `fallback_ready`.
It never invokes old bootstrap/migrate/harden, starts a worker, changes Caddy or
restores an environment file. The enclosing controller may restore route/config
and stop the failed candidate only after that success. Failure retains the
candidate; Caddy reload failure is not recorded as a successful rollback and also
retains it. The first successful source-controller handoff preserves the prior
authenticated manifest only when it matches the actual active immutable image;
retries cannot replace that fallback evidence with a failed candidate manifest.

This guardian operation is intentionally disable-only and can revoke existing
guardian authority/credentials according to the existing release contract. It
does not restore relationships or grant new activation. Any fallback whose signed
policy expects enabled intake requires a separately reviewed operational path.
Privacy-worker flags remain false and existing installed restore/retirement
controls must remain in force.

## Verification and limits

Run `python3 deployment/precision-admission_test.py` and
`sh deployment/pull-release_test.sh`; the former uses real temporary files/locks
and the actual adapter with synthetic process commands, and the latter verifies
normal/legacy source controller ordering and failure behavior. Its controller
fixtures intentionally do not claim to exercise production root ownership.
Strict host ownership/path tests are in the separate admission suite.

These local tests are not an installed-system rehearsal. Before any release,
integrated actual C startup and routed protected requests must be exercised with
the final P schema fixture, real admitted/block/restore transitions, guardian
binding false and privacy retired. Demonstrate no old reader serving partial/v2
data and no old migration on fallback. A real signed image, installed-controller
bounded patch review, exact host-role/endpoint evidence and final human release
authority remain required. No live, cloud, secret, GitHub or publication action
was performed to produce this source change.

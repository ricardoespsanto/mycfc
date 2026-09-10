# Privacy restore replay

This command is an offline, database-only entry point for an isolated restore
drill. It never lists ledger objects, calls AWS, deletes object-store versions,
or contacts providers. The operator must prefetch and independently verify exact
immutable object versions before invocation.

```sh
DATABASE_URL=postgres://... privacy-restore-replay \
  --isolated-restore \
  --ledger-input /input/ledger.json \
  --private-key-file /run/secrets/tombstone-replay-key \
  --attestation-output /output/replay.json \
  --policy-version privacy-policy-v1 \
  --executor-version privacy-erasure-executor/v2 \
  --plan-schema-version privacy-erasure-plan/v2 \
  --image-digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

The private-key file must be an owner-only regular file containing one
base64-encoded 32-byte X25519 private key. The output path must not already
exist. The explicit isolation flag is mandatory.

When an isolated first-activation database has no live ledger objects, create
one protected, non-identifying synthetic fixture and its input inventory:

```sh
DATABASE_URL=postgres://... privacy-restore-replay \
  --isolated-restore \
  --bootstrap-synthetic-fixture \
  --private-key-file /run/secrets/tombstone-replay-key \
  --synthetic-ledger-output /output/synthetic-ledger.json
```

The resulting inventory must then be processed by the ordinary invocation
above. Bootstrap does not itself replay data or create an attestation. A
synthetic inventory is valid only inside an isolated restore and every selected
record must carry the authenticated
`mycfc/privacy-restore-synthetic-fixture/v1` marker.

## Input contract

The maximum input size is 32 MiB with at most 1,024 objects. Unknown JSON
fields, duplicate exact object versions, malformed hashes, inconsistent sizes,
and verification timestamps earlier than object write time fail closed.

```json
{
  "contract": "mycfc/privacy-restore-ledger-input/v2",
  "source": "LIVE_LEDGER",
  "inventory_sha256": "lowercase SHA-256 hex",
  "objects": [
    {
      "key_sha256": "lowercase SHA-256 hex",
      "object_version": "opaque exact version",
      "ciphertext_sha256": "lowercase SHA-256 hex",
      "size_bytes": 123,
      "payload": "base64",
      "written_at": "2026-09-10T10:00:00Z",
      "verified_at": "2026-09-10T10:00:01Z",
      "retain_until": null
    }
  ]
}
```

`written_at` is the exact-version S3 `LastModified`. `verified_at` is the
offline observation time at which the drill verified the exact version's head
and checksum; it is intentionally not the original broker receipt time.
Closure objects require the exact `ObjectLockRetainUntilDate` in
`retain_until`; intent objects require null or an omitted field.

`inventory_sha256` is SHA-256 of compact JSON for the metadata-only object
array, excluding `payload`. Objects are sorted by `key_sha256` then
`object_version`; each object has sorted keys
`ciphertext_sha256,key_sha256,object_version,retain_until,size_bytes,verified_at,written_at`.
Timestamps are normalized to UTC RFC3339Nano with `Z`, and intent
`retain_until` is JSON null.

`source` is exactly `LIVE_LEDGER` or `SYNTHETIC_BOOTSTRAP`; mixing authenticated
synthetic and live records fails closed. V2 envelope discovery fields are not
duplicated in inventory metadata. The
command derives kind, locator key ID, and 32-byte locator digest from the
encrypted envelope's bounded discovery header, then binds and cross-checks all
three through AEAD additional authenticated data before import. Strictly shaped
v1 envelopes are recognized only to reject the whole inventory before the
database boundary; they are never imported or replayed, and every successful
result therefore has `non_replayable_v1_count` zero.

The command authenticates and selects the entire inventory before crossing a
database boundary. Every selected entry must be a current
`restore-tombstone-closure/v4` containing the exact
`relational-erasure-replay/v1` prescription and authenticated
`mycfc/membership-history-postcondition/v1` digest. Intent-only records and
readable closure-v1 through closure-v3 records remain decryptable for diagnosis,
but make the complete inventory ineligible and cause zero replay calls or
attestation writes.

## Output contract

On complete success the command exclusively creates a mode `0600` JSON file:

```json
{
  "contract": "mycfc/privacy-restore-replay-result/v2",
  "result": "SUCCEEDED",
  "input_source": "LIVE_LEDGER",
  "policy_version": "privacy-policy-v1",
  "executor_version": "privacy-erasure-executor/v2",
  "plan_schema_version": "privacy-erasure-plan/v2",
  "image_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "schema_migration_digest": "lowercase SHA-256 hex",
  "inventory_sha256": "lowercase SHA-256 hex",
  "object_count": 1,
  "imported_count": 1,
  "replayed_count": 1,
  "already_applied_count": 0,
  "non_replayable_v1_count": 0,
  "absence_verified_count": 1,
  "synthetic_replayed_count": 0,
  "closure_v4_count": 1,
  "intent_only_count": 0,
  "legacy_closure_v2_count": 0,
  "erasure_effective_at_verified_count": 1,
  "membership_postcondition_contract": "mycfc/membership-history-postcondition/v1",
  "membership_postcondition_sha256": "lowercase SHA-256 hex",
  "membership_postcondition_verified_count": 1,
  "membership_count": 1,
  "variation_count": 1,
  "failed_count": 0
}
```

The result contains counts and digests only. It never contains source
execution, request, subject, locator, object-key, or plaintext values. Any
selected current erasure that cannot be authenticated, imported, replayed, and
verified prevents creation of a success attestation. Current activation
evidence additionally requires `intent_only_count`, `legacy_closure_v2_count`,
`non_replayable_v1_count`, and `failed_count` to be zero;
`closure_v4_count`, `erasure_effective_at_verified_count`,
`membership_postcondition_verified_count`, and `absence_verified_count` must
each equal `replayed_count`. The aggregate membership postcondition digest is
the SHA-256 of the version frame followed by the sorted authenticated 32-byte
per-replay digests; counts cover the exact captured historical membership and
retained variation rows.

`schema_migration_digest` is SHA-256 of the UTF-8 migration versions returned
by `SELECT version FROM mycfc_meta.schema_migrations ORDER BY version`, joined
with a single newline and no trailing newline.

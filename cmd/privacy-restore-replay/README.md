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
  --attestation-output /output/replay.json
```

The private-key file must be an owner-only regular file containing one
base64-encoded 32-byte X25519 private key. The output path must not already
exist. The explicit isolation flag is mandatory.

## Input contract

The maximum input size is 32 MiB with at most 1,024 objects. Unknown JSON
fields, duplicate exact object versions, malformed hashes, inconsistent sizes,
and verification timestamps earlier than object write time fail closed.

```json
{
  "contract": "mycfc/privacy-restore-ledger-input/v1",
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

V2 envelope discovery fields are not duplicated in inventory metadata. The
command derives kind, locator key ID, and 32-byte locator digest from the
encrypted envelope's bounded discovery header, then binds and cross-checks all
three through AEAD additional authenticated data before import. Strictly shaped
v1 envelopes can only increment `non_replayable_v1_count`; they are never
imported or replayed.

## Output contract

On complete success the command exclusively creates a mode `0600` JSON file:

```json
{
  "contract": "mycfc/privacy-restore-replay-result/v1",
  "result": "SUCCEEDED",
  "schema_migration_digest": "lowercase SHA-256 hex",
  "inventory_sha256": "lowercase SHA-256 hex",
  "object_count": 1,
  "imported_count": 1,
  "replayed_count": 1,
  "already_applied_count": 0,
  "non_replayable_v1_count": 0,
  "absence_verified_count": 1,
  "failed_count": 0
}
```

The result contains counts and digests only. It never contains source
execution, request, subject, locator, object-key, or plaintext values. Any
selected v2 erasure (the closure supersedes its matching intent) that cannot be
authenticated, imported, replayed, and verified prevents creation of a success
attestation.

`schema_migration_digest` is SHA-256 of the UTF-8 migration versions returned
by `SELECT version FROM mycfc_meta.schema_migrations ORDER BY version`, joined
with a single newline and no trailing newline.

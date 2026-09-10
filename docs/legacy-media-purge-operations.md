# Legacy media inventory and purge

This one-time mechanism inventories or permanently removes every object version and delete marker under the fixed `profiles/`, `repairs/`, and `equipment/` prefixes. It does not classify retained images, rewrite retained objects, inspect database ownership, or establish any provider/legal fact. Because current uploads use the same prefixes, an execution would cover both historical and current objects. Do not execute it while the application can write media or while any retained image must survive.

The command ships in dry-run mode and destructive execution is source-disabled by `legacyMediaPurgeExecutionEnabled = false`. Changing that gate, building and releasing the changed source, granting the temporary version-list/delete capability, stopping media writes, and running a live purge are separate reviewed changes. None is performed by this release.

## Dry-run inventory

Use a dedicated random evidence HMAC key. Do not reuse an upload, object-target, session, CSRF, or application verification key. Supply it through the process environment rather than a command argument:

```sh
export LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID='legacy-media-inventory-2026-09'
export LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64='<base64 of at least 32 random bytes>'
go run ./cmd/legacy-media-purge
```

The standard AWS credential chain, `AWS_REGION`, and `S3_BUCKET_NAME` select the storage account and bucket. The emitted JSON contains only fixed prefix names, counts, list-call/stability counters, a key identifier, and keyed inventory digests. It never emits raw object keys or provider version identifiers. Store the complete JSON with the change record and compare a second dry run: changed counts or digests mean the inventory is not quiescent.

Dry run requires version-list access only. It performs no deletion and is the default even if the command is passed no flags.

## Separately approved execution

Execution must remain blocked until all of these are independently confirmed:

1. The product/release authority has approved deletion of every object in all three prefixes, including objects still referenced by application rows.
2. Media writes and relevant background work are stopped, and the first and second dry-run evidence agree.
3. The internal source gate has been changed in a reviewed release from `false` to `true`.
4. The one-time identity has only version listing for the selected bucket and version deletion for the three fixed prefixes; ordinary application credentials do not receive those permissions.
5. The operator copies the aggregate `inventory_digest` from the approved dry run and types the exact execution phrase shown below. Changed inventory blocks deletion; a generic `yes` is intentionally rejected.

```sh
go run ./cmd/legacy-media-purge --execute --inventory-digest '<approved 64-character inventory_digest>' --confirm DELETE-ALL-LEGACY-MEDIA-VERSIONS
```

The runner deletes explicit key/version pairs in batches of at most 1,000. It then performs complete authoritative prefix relists until each prefix has two consecutive empty scans. A version that was not in the initial inventory, a version appearing after an empty scan, incomplete provider acknowledgements, pagination failure, or a safety bound stops the run without claiming success. Rerunning after an interruption is idempotent: already absent versions are not treated as failures, and counts describe only deletions confirmed in that invocation.

Each prefix is limited to 1,000 listing pages, 250,000 entries per full scan, and 10 verification passes. Reaching a bound is a failed/incomplete operation, not an empty result. Investigate through privacy-safe aggregate evidence; never paste raw keys, version identifiers, SDK errors, or bucket responses into logs or tickets.

After a successful execution, retain the execution JSON and run one final dry-run using the same evidence key. It must report zero versions and zero delete markers for all three prefixes. Database pointer reconciliation, retained-image treatment, normal privacy execution, and worker activation remain separate work.

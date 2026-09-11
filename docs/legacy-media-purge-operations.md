# Legacy media inventory and purge

This one-time mechanism inventories or permanently removes every object version and delete marker under the fixed `profiles/`, `repairs/`, and `equipment/` prefixes. It does not classify retained images, rewrite retained objects, inspect database ownership, or establish any provider/legal fact. Because current uploads use the same prefixes, an execution would cover both historical and current objects. Do not execute it while the application can write media or while any retained image must survive.

Every ordinary build compiles `execution_gate_disabled.go`, so destructive execution remains unavailable. The reviewed execution artifact target and dedicated `Dockerfile.legacy-media-purge` explicitly opt into the `legacy_media_purge_execute` build tag; only the latter is eligible for the protected publishing workflow. Its purge-only image contains no application server, migration, database, worker, or shell executable; its immutable `purge-<SHA>` tag is outside the `release-*` namespace selected by the production deployment agent. Publishing that image, provisioning inventory permission, adding the write fence and deletion permission, stopping media writes, executing the purge, resetting/reconciling the database, and revoking the temporary identity remain separate reviewed changes.

Production Terraform defaults the identity, bucket write fence, and deletion gates to `false`. The first creates a temporary IAM user whose identical inline policy and permissions boundary permit only prefix-scoped `s3:ListBucketVersions`. The write fence temporarily denies every principal both `s3:PutObject` and ordinary `s3:DeleteObject` under the three prefixes, preventing new versions and delete markers; deletion cannot be enabled without it and adds only `s3:DeleteObjectVersion` after the fence is installed. Every allowance and the deny fence expire automatically at the same valid future deadline, which Terraform limits to no more than 24 hours after plan time. Terraform never creates an access key.

## Dry-run inventory

Use a dedicated random evidence HMAC key. Do not reuse an upload, object-target, session, CSRF, or application verification key. For local rehearsal, supply it through the process environment rather than a command argument:

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
3. The dedicated purge-only image was built by CI, published from an exact tested `main` commit by the protected manual workflow, and has GitHub-signed provenance bound to that workflow, source commit, and immutable digest. The ordinary application image remains source-disabled.
4. The one-time identity has only version listing for the selected bucket and version deletion for the three fixed prefixes; ordinary application credentials do not receive those permissions.
5. The operator copies the aggregate `inventory_digest` from the approved dry run. The host wrapper supplies the program's fixed execution phrase; changed inventory blocks deletion and no interactive or generic `yes` confirmation is accepted.

The production wrapper supplies the exact confirmation internally only for its `execute` mode and accepts only an immutable `mycfc-production@sha256:...` image. It requires GitHub CLI attestation verification, binds the image to the protected workflow and exact source commit, verifies the image revision and exact temporary principal ARN, shares the release lock, and refuses to run while the release service/timer, privacy worker, or any legacy/blue/green application container is active. It runs as the non-root distroless UID with a read-only filesystem, drops every capability, disables instance-metadata credentials, mounts only the purge credential and evidence-key files, validates an exact reconciled evidence schema, atomically refuses evidence replacement, and logs only fixed lifecycle codes plus aggregate counts.

The runner deletes explicit key/version pairs in batches of at most 1,000. It then performs complete authoritative prefix relists until each prefix has two consecutive empty scans. A version that was not in the initial inventory, a version appearing after an empty scan, incomplete provider acknowledgements, pagination failure, or a safety bound stops the run without claiming success. Rerunning after an interruption is idempotent: already absent versions are not treated as failures, and counts describe only deletions confirmed in that invocation.

Each prefix is limited to 1,000 listing pages, 250,000 entries per full scan, and 10 verification passes. Reaching a bound is a failed/incomplete operation, not an empty result. Investigate through privacy-safe aggregate evidence; never paste raw keys, version identifiers, SDK errors, or bucket responses into logs or tickets.

After a successful execution, retain the execution JSON and run one final dry-run using the same evidence key. It must report zero versions and zero delete markers for all three prefixes. Database pointer reconciliation, retained-image treatment, normal privacy execution, and worker activation remain separate work.

## Reviewed production sequence

1. Choose a short UTC maintenance window of no more than 24 hours and set `legacy_media_purge_permission_expires_at` to its end. Apply only `legacy_media_purge_identity_enabled=true`; keep the write fence and deletion false. Review the plan and prove the web, release, backup, and worker identities are unchanged.
2. Manually run **Publish legacy media purge image** for the exact CI-tested 40-character `main` SHA. Record the resulting immutable image digest and verify its GitHub attestation; never retag it as `release-*`. Ensure a current GitHub CLI is installed on the host before the maintenance window.
3. Only after image publication and verification, create one access key outside Terraform for the output `legacy_media_purge_user_name`. Install it as profile `mycfc-legacy-media-purge` in `/etc/mycfc/legacy-media-purge/aws-credentials`. Generate the separate evidence key in `/etc/mycfc/legacy-media-purge/evidence.key`. Create `/etc/mycfc/legacy-media-purge/environment` with `AWS_REGION`, `S3_BUCKET_NAME`, the exact production `ECR_REPOSITORY_URL`, `LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID`, the tested commit as `LEGACY_MEDIA_PURGE_EXPECTED_SHA`, and the exact Terraform output `legacy_media_purge_user_arn` as `LEGACY_MEDIA_PURGE_EXPECTED_PRINCIPAL_ARN`. The directory is `root:65532` mode `0750`; all three files are `root:65532` mode `0440`. Create `/var/lib/mycfc/legacy-media-purge` as `root:root` mode `0700`; the wrapper accepts only a new direct-child JSON file and publishes it atomically without replacement.
4. Disable and stop `mycfc-pull-release.timer` and `mycfc-pull-release.service`, stop the legacy and both blue/green application slots, and confirm the privacy worker and every media-writing background process are inactive. This intentionally causes a maintenance outage.
5. Run two inventories with the same key and immutable image. Each output path must be new and inside a root-only evidence directory:

   ```sh
   /opt/mycfc/deployment/run-with-cloudwatch-logs.sh \
     /opt/mycfc/deployment/legacy-media-purge.sh inventory \
     '<repository>@sha256:<digest>' \
     '/var/lib/mycfc/legacy-media-purge/inventory-1.json'
   ```

   Compare the complete files. The aggregate and each per-prefix version count, delete-marker count, and inventory digest must agree. CloudWatch receives phase/result codes and aggregate counts; the root-only files remain the authoritative detailed evidence.
6. Apply `legacy_media_purge_write_fence_enabled=true` and `legacy_media_purge_deletion_enabled=true` together without changing the identity, prefix set, expiry, or any unrelated resource. Review the exact bucket deny, inline policy, and permissions boundary before continuing. Terraform orders the version-deletion grant after the fence. The expiring bucket deny prevents any principal from creating either a new object version or delete marker in the purge prefixes even if a writer is accidentally restarted.
7. Read the approved aggregate digest from the evidence file, then run the execution mode once:

   ```sh
   /opt/mycfc/deployment/run-with-cloudwatch-logs.sh \
     /opt/mycfc/deployment/legacy-media-purge.sh execute \
     '<repository>@sha256:<digest>' \
     '<approved 64-character inventory_digest>' \
     '/var/lib/mycfc/legacy-media-purge/execution.json'
   ```

8. Require `mode=EXECUTE`, matching initial/deleted counts, and at least two stable empty scans for every prefix. Run a third inventory to a new file and require zero versions and zero delete markers for every prefix.
9. Because the current production rows are approved disposable test data, reset and reapply the reviewed database baseline before restarting either application slot. Do not restart an application that still contains a pointer to a purged object.
10. Delete the temporary AWS access key, remove the protected host credential and evidence-key inputs, and atomically apply the deletion, write-fence, and identity gates false with `legacy_media_purge_permission_expires_at=null`. Verify the temporary user, inline policy, boundary, and bucket deny are absent. Terraform removes deletion permission before the fence; an already expired deadline does not block this inert teardown. Re-enable the release timer and restore the application only after the fresh database and readiness checks pass.

The IAM expiry limits a leaked key but does not replace revocation. Preserve the three inventories, execution evidence, exact image digest, reviewed plans/applies, and key identifier in restricted off-host operational records; do not place the HMAC key, access key, raw object data, or Terraform state in GitHub or CloudWatch.

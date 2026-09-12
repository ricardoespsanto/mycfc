# Guardian-authority V2 operations

Guardian-authority V2 is shipped fail closed. Migration `202609120005_guardian_authority_activation` invalidates privacy-worker activation evidence, disables every guardian policy and clears the `guardian-intake-v2` release binding. Every supported image release also calls a separate disable-only database boundary before starting or switching to the candidate: it revokes current guardian access and binds the database to the verified candidate image and embedded migration inventory, but cannot create approval evidence or enable intake. The ordinary release, installer and migration paths do not create the guardian operator login, read an approval, run preflight or enable intake. Production activation is a later, explicit human release gate.

## Control contract

One active adult platform administrator is the controller-authorized operator. The same opaque user UUID must appear in the protected operator environment and the controller approval. It is not a second-person workflow. Enabling requires all of these facts to agree atomically:

- the fixed `guardian-authority-v2` evidence categories, reason codes and numeric limits already accepted for #280;
- a non-placeholder policy version and SHA-256 digest of the exact canonical policy object;
- an opaque controller approval reference, approval date and `CLUB_DIRECTION` role;
- an opaque legal reviewer reference, legal-review evidence reference, review date and `APPROVED` conclusion;
- an effective date no later than the current Lisbon calendar date and a review-due date later than both the effective date and current Lisbon date;
- the exact active administrator actor, immutable running image digest, expected database name and ordered embedded migration digest through migration 005;
- no enabled conflicting policy and, while disabled, no verified/suspended relationship of any policy version and no associated or dangling dependent credential or indexed session. Pending applications without credentials remain allowed.

The web database role has no usage on `guardian_ops`, cannot read approval evidence and cannot call status, preflight, enable or disable. The dedicated `mycfc_guardian_activation_operator` login has no public-schema or table access and may call only those four security-definer routines. A different fixed `mycfc_guardian_release_bind` login may call only `release_disable_and_bind`; it cannot call status, preflight, enable or operator disable, read either operator table, use the public or metadata schemas, inherit another role, create objects, or own the security-definer routine. The one-shot release-bind container receives only this credential, the expected database and candidate digest: it receives no migration/bootstrap URL, AWS configuration, approval file or activation credential. The object-owner identity remains offline from that runtime container. Routine deployment never provisions or rotates either isolated password.

## Protected inputs

The supported release path also requires `/etc/mycfc/guardian-release-bind.env`, root-owned mode `0600`, containing exactly `GUARDIAN_RELEASE_BIND_DATABASE_URL` for the fixed `mycfc_guardian_release_bind` login and `GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE`. Keep it out of `/etc/mycfc/mycfc.env`, AWS application secrets and the activation file. Creating or rotating that login is a separate root-approved provisioning operation:

```sh
sudo /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-release-bind.sh provision
```

The command uses the bootstrap administrator only in the dedicated provisioning container. Provisioning is one transaction and is safe to repeat. Before migration 005 exists it only creates or re-hardens the fixed login: it strips memberships, removes database create/temporary and known schema/object privileges, and grants only database connect. It neither depends on `guardian_ops` nor leaves a partially configured login if a statement fails. Migration 005 detects that staged login after creating `guardian_ops`, reasserts every object denial and grants only `release_disable_and_bind`; the post-migration hardening pass reasserts the same boundary.

On the first rollout that introduces this command, unpack the reviewed deployment bundle and create the protected release-bind environment before running `install.sh`. Obtain the already-published candidate's exact immutable image reference through the ordinary release evidence, then stage the login against the still-004 database with this explicit bootstrap form; the wrapper rejects mutable references:

```sh
sudo env MYCFC_GUARDIAN_RELEASE_BIND_PROVISION_IMAGE='<repository>@sha256:<64-lowercase-hex>' \
  /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-release-bind.sh provision
```

Only after that command succeeds may `install.sh` start the normal release service. Its executable order is `db-bootstrap` → migration 005 → post-migration hardening → `guardian-release-bind` → candidate start. Later rotations can use the currently installed immutable image from the main environment. The normal `guardian-release-bind` release service loads only the resulting narrow login file. It never falls back to `DATABASE_URL`, the migration URL or the database object owner. A release fails before candidate start if the file or login is absent; it must not be repaired by copying migration credentials into the file.

On the production host, create these only during a separately approved activation window:

- `/etc/mycfc/guardian-activation.env`, root-owned mode `0600`, containing exactly `GUARDIAN_ACTIVATION_DATABASE_URL`, `GUARDIAN_ACTIVATION_EXPECTED_DATABASE` and `GUARDIAN_ACTIVATION_ACTOR_REF`;
- `/etc/mycfc/guardian-activation/`, root-owned mode `0700`;
- `/etc/mycfc/guardian-activation/approval.json`, root-owned mode `0600`, a single canonical JSON object matching `mycfc/guardian-authority-policy-approval/v1`, including `expected_database` equal to the protected operator environment.

Do not put a name, email, case detail or evidence document in the approval file. Controller and legal references must be opaque locators to evidence retained outside MyCFC. The repository deliberately contains no real actor, reference, date, policy version, password or signed approval.

The deterministic canonical algorithm is `mycfc/guardian-authority-policy-approval/v1`: UTF-8; no byte-order mark or trailing newline in the protected file; no insignificant whitespace; Go JSON string escaping with HTML escaping disabled; and fields in the exact order emitted by `guardian-activation generate` (`contract`, actor, database, controller fields, legal fields, fixed `policy`, policy hash, version). The nested policy is the fixed compact object implemented by the generator and migration. The database retains the raw text argument, reconstructs those exact bytes independently, compares both top-level and nested policy bytes, and hashes only after equality. JSONB normalization therefore cannot make reordered or whitespace-modified input acceptable.

Generate the file offline from reviewed, non-secret opaque references rather than hand-ordering JSON. This example intentionally contains placeholders and must not be run unchanged:

```sh
umask 077
go run ./cmd/guardian-activation generate \
  --actor-ref '<active-admin-uuid>' --expected-database '<database>' --policy-version '<approved-version>' \
  --controller-approval-reference '<opaque-controller-ref>' --controller-approved-on '<YYYY-MM-DD>' \
  --effective-on '<YYYY-MM-DD>' --review-due-on '<YYYY-MM-DD>' \
  --legal-reviewer-reference '<opaque-reviewer-ref>' --legal-review-reference '<opaque-legal-evidence-ref>' \
  --legal-reviewed-on '<YYYY-MM-DD>' >approval.json
```

The generator fixes the contract, role, conclusion, policy values and policy SHA-256; validates identifiers, UUID, references and date ordering; and writes exactly the canonical JSON bytes with no trailing newline. Redirect its output directly as shown; do not pipe it through a formatter or line-oriented writer. Transfer it through the approved custody channel, install it root-owned mode `0600`, and compare its SHA-256 outside MyCFC. `preflight` independently recomputes the policy and approval hashes; it never accepts a supplied approval hash.

## Staged rollout

These commands are examples only. They require an approved production maintenance gate and root access; source acceptance or deployment approval does not authorize them.

1. Before the first supported release, create the fixed root-only file and run the immutable-candidate provisioning command above against exact schema 004. Verify the privacy-safe `event=guardian_release_bind_role_provisioned` record, then run `install.sh` or the approved release service. Migration 005 must detect the staged role, grant only the cutoff routine and leave guardian intake, every guardian policy and privacy activation disabled. Post-migration hardening must complete before the release-bind call, and the deployment log must order `database_migrate` → `database_harden` → `guardian_release_bind` → `candidate_start`. Any failure stops before candidate start or traffic switch. Reprovisioning or password rotation is never automatic.
2. Install the protected operator environment and canonical approval using the ownership/modes above. Confirm `MYCFC_IMAGE` in `/etc/mycfc/mycfc.env` is an immutable `@sha256:` reference and the active container is running that exact reference.
3. Provision or rotate the isolated login once:

   ```sh
   sudo /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-activation.sh provision
   ```

4. Record the disabled baseline and aggregate impact counts:

   ```sh
   sudo /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-activation.sh status
   ```

5. Run preflight. Exit status `3` means a prerequisite or impact check is blocked; do not bypass it or edit the database manually:

   ```sh
   sudo /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-activation.sh preflight
   ```

6. Stop for the final human activation decision. Only after that separate approval, run `enable` through the same CloudWatch wrapper. Repeat `status`, verify `state=ENABLED` and all four current-binding booleans are true, then exercise one synthetic/non-personal intake check. Repeating the exact enable is a no-op (`changed=false`); a different binding is rejected.

The CloudWatch deployment stream receives only fixed event names, booleans, hashes and aggregate relationship/session/credential/invitation counts. It never receives the actor UUID, policy version, controller/legal references, account identifiers, names, emails or approval JSON. The immutable database approval record retains the exact canonical bytes and bindings for controller evidence.

## Emergency disable and rollback

The safe rollback is destructive to active guardian access by design:

```sh
sudo /opt/mycfc/deployment/run-with-cloudwatch-logs.sh /opt/mycfc/deployment/guardian-activation.sh disable
```

Only the exact operator actor bound by the stored approval may invoke it. Disable first clears the intake binding and disables the active policy, then reconciles every verified/suspended relationship, clears dependent login credentials, advances credential versions and deletes indexed dependent sessions. The command reports only aggregate affected counts and appends a new immutable audit row on every invocation. Repeating it with the same bound operator is safe and auditable. Credentials and relationships are not automatically restored; a later re-enable requires a clean preflight and affected relationships require the ordinary reviewed workflow.

After any release containing a later database migration, assume guardian and privacy activation are invalid until new current evidence passes their respective preflights. Never work around a blocked status by granting the web role, changing the gate row, enabling a policy directly or reusing stale image/schema evidence.

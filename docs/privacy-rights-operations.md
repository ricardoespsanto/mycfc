# Privacy rights and data maintenance

MyCFC does not accept or execute privacy-rights requests inside the application. The retired request, reviewer, executor, activation, receipt, restore and scheduled-retention control planes must not be restarted or reprovisioned.

## Rights requests

The authoritative public instructions are at `/legal/direitos`. A person may contact Clube Fluvial de Coimbra by the published email address or by letter. An authorized club officer handles identity checks, correspondence, scope, legal review and evidence outside MyCFC. Do not copy identity documents, free-form case notes or legal correspondence into the application database.

When a request requires a database or object-store change, prepare a request-specific runbook and obtain a separate human approval for the exact records and live environment. Record the approver, operator, time, reason, affected categories, before/after aggregate counts and verification result in the club's approved evidence location. Never record the person's data, object keys, credentials or raw database output in deployment logs.

The #335 age-exception provenance and #336 append-only sporting-correction audit families now have the approved #334 **full-deletion** disposition, implemented locally in migration `202610010001_manual_classification_erasure` and the fresh baseline. Delete affected audit events completely and clear the complete membership reason/actor/time triple; retain no reason fragment or pseudonym. Membership identities, dates, teams, categories, event/training history, published prescriptions and current sporting selections remain unchanged. This is a classification-only manual capability, not whole-account erasure or a general retention policy. Source delivery is not approval to apply a live migration or process a real case.

Follow [the manual classification-erasure runbook](manual-classification-erasure.md) after the separate external approval in paragraph 9. Target subject/actor rows are selected automatically; exact reviewed IDs with expected subjects must additionally cover approved grant-linked and identifying third-party reason rows. This review is not solved by name matching or the database checker. The private operation/checker are unavailable to PUBLIC, web and retired roles and use the existing trusted offline migration-owner capability. Protected erased-state preserves existing category mismatches without allowing new exceptions; append-only and membership/history guards remain active for ordinary writers. Never disable triggers or reactivate #318.

The retained `classification_erasure.erased_memberships.membership_id` is restricted **linkable personal metadata**: its foreign key joins to `user_memberships`, including `user_id` and classification. Memberships and this marker remain to preserve valid historical classification; the full-deletion choice covers the approved reasons/events, not this retained metadata. This is neither anonymous data nor pseudonymous whole-account erasure. The marker follows the surviving membership lifecycle; its exact retention/disposition requires the existing retention decisions and a separately approved manual case review before real-user adoption. This source-only draft establishes no new retention term or adoption approval. Include memberships and markers in backup/restore inventory and approved reconciliation; zero reasons/events is not blanket absence of personal data.

Staff must still record only brief operational reasons, not names, birth dates, medical facts or another person's identifiers, and never log reasons or linked identities. This is guidance, not an enforced content classifier: trimming/length checks do not prove free-text minimization. The audit's existence does not grant permission to read it; retain the current staff/subject/grant authorization boundary.

Record only aggregate before/after counts and verification in approved external evidence. Separately inventory remaining category-definition approver references without deleting shared definitions or grants. The checker verifies only the target and explicit reviewed scope; transaction locks fence concurrent writes until commit, not indefinitely. Drain classification writers for the approved maintenance window. There is no automatic durable erasure/tombstone ledger.

Before a restored database can serve users, keep it isolated and manually reconcile every applicable previously approved classification disposition with the same operation and authenticated external scope manifest, then require zero remaining target/explicit affected audit events and exception provenance. The synthetic regression takes a real populated pre-erasure `pg_dump`, restores it into a non-serving database and verifies manual deletion plus unchanged histories/snapshots. The generic PostgreSQL restore drill checks schema and retired-control flags, **not** this case-specific non-resurrection condition. Backup restoration and any live rights action still require separate approval.

## Media cleanup

`media-cleanup` is the only continuous privacy-adjacent maintenance service. It processes the existing upload-provenance cleanup queue for removed, replaced, failed or abandoned profile, repair and equipment photos. Its database role can call only the three `media_upload_cleanup_*` routines. Its object-store identity is limited to version inventory and exact version deletion under `profiles/`, `repairs/` and `equipment/`; it cannot read object bodies, create unversioned delete markers, upload objects or assume another role.

The host service uses `/etc/mycfc/media-cleanup.env`. Keep that file root-owned with mode `0600`. Cleanup logs and evidence must contain fixed event codes and aggregate counts only.

## Manual data retention

Retention is an explicit one-shot operation; there is no timer. Run it only after the responsible officer has reviewed the due categories and approved the operation:

Create `/etc/mycfc/data-retention.env` as `root:root` mode `0600` with `DATA_RETENTION_ENABLED=true` and the dedicated `DATA_RETENTION_DATABASE_URL`. Then run the binary from the exact signed image already selected on the host:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env \
  -f /opt/mycfc/deployment/compose.yaml \
  --profile manual-maintenance run --rm data-retention
```

The dedicated login inherits only `mycfc_data_retention`, whose callable surface is `data_retention_run(uuid,integer)` and `data_retention_status()`. Capture the same owner, approval, aggregate and verification evidence required for a rights-request change. A failed or ambiguous run is not approval to retry with broader credentials.

## Retirement and rollback posture

Migration `202609220001_privacy_automation_retirement` is a permanent fence. It disables intake and worker activation, engages the kill switch, revokes automation routines, removes memberships, makes legacy roles `NOLOGIN`, and rejects attempts to reactivate them. Historical tables remain for migration compatibility but are not an operational workflow.

During host upgrade, `retire-privacy-automation.sh` disables and removes only the named legacy units and containers. It reports legacy credential and state paths for an operator to review; it does not delete those files. The current production environment contains no real user data, so #318 treats the retired privacy ledger, receipt, activation and worker resources as disposable data infrastructure. Terraform apply and actual cloud deletion still require a separately reviewed destructive plan and explicit infrastructure approval. Database rollback must never remove the retirement fence or restore an automation credential.

# Privacy rights and data maintenance

MyCFC does not accept or execute privacy-rights requests inside the application. The retired request, reviewer, executor, activation, receipt, restore and scheduled-retention control planes must not be restarted or reprovisioned.

## Rights requests

The authoritative public instructions are at `/legal/direitos`. A person may contact Clube Fluvial de Coimbra by the published email address or by letter. An authorized club officer handles identity checks, correspondence, scope, legal review and evidence outside MyCFC. Do not copy identity documents, free-form case notes or legal correspondence into the application database.

When a request requires a database or object-store change, prepare a request-specific runbook and obtain a separate human approval for the exact records and live environment. Record the approver, operator, time, reason, affected categories, before/after aggregate counts and verification result in the club's approved evidence location. Never record the person's data, object keys, credentials or raw database output in deployment logs.

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

# Production deployment

MyCFC runs as a pull-based blue/green deployment. A green push to `main` does not deploy. An approved signed release publishes an immutable image and manifest; the host release service verifies both before migration, candidate checks and traffic switching.

## Host installation

Install Docker, the AWS CLI and systemd, then run `deployment/install.sh` as root from the reviewed release bundle. The installer copies the deployment scripts and units, validates protected files, installs the release poller, nightly PostgreSQL backup and media cleanup service, and invokes `retire-privacy-automation.sh` to disable and remove the exact retired privacy units and containers.

The retirement helper deliberately does not delete legacy credentials or state. Review every reported path and remove it only under a separate host-maintenance approval.

The bootstrap environment is `/etc/mycfc/mycfc.env`, owned by root with mode `0600`. It contains host, AWS and backup inputs but no database passwords. The application password is loaded from the application-only Secrets Manager value. Keep `POSTGRES_PASSWORD` and `MIGRATION_DB_PASSWORD` only in `/etc/mycfc/database-control.env`, a root-owned `0600` file mounted into PostgreSQL and the one-shot release database jobs but never into the web containers. `POSTGRES_RESTORE_VERIFICATION_ENABLED=true` is valid only with `BACKUP_MANIFEST_AUTH_ENABLED=true`; the installer rejects any configuration that would schedule unauthenticated restore verification.

Required backup controls include:

```text
BACKUP_S3_BUCKET=<bucket>
BACKUP_KMS_KEY_ID=<key>
BACKUP_NONCURRENT_CLEANER_ENABLED=false
BACKUP_NONCURRENT_CLEANER_DRY_RUN=true
BACKUP_MANIFEST_AUTH_ENABLED=true
POSTGRES_RESTORE_VERIFICATION_ENABLED=true
```

The exact-version backup cleaner remains separately gated and uses a temporary, role-bound session. The annual isolated PostgreSQL restore verification checks authenticated backups and applies current migrations; it has no privacy-ledger replay or activation function.

## AWS configuration

Apply `infra/environments/production` only after review and infrastructure approval. Terraform provisions the application runtime configuration, app secrets, SES, ECR, deployment logs, backup resources and dedicated identities.

The application runtime identity may read `/mycfc/production/*`, `/mycfc/production/app-runtime-secrets-v2` and the application media bucket. It is explicitly denied access to the retired `/mycfc/production/app-secrets` secret, including historical versions that may contain former privileged database credentials. It cannot use ECR, deployment logs or delete media objects. The release identity may read the immutable release image and write allowlisted deployment events; it cannot read application configuration or use S3.

The media cleanup identity is separate. It can list versions and delete only explicitly versioned objects under `profiles/`, `repairs/` and `equipment/`; it cannot read object bodies, create unversioned delete markers, upload, or chain roles. Keep both maintenance database logins only in `/etc/mycfc/database-maintenance.env`, a root-owned `0600` file accepted by the release bootstrap and migration jobs. The file must contain exactly `MEDIA_CLEANUP_DB_USER`, `MEDIA_CLEANUP_DB_PASSWORD`, `DATA_RETENTION_DB_USER`, and `DATA_RETENTION_DB_PASSWORD`. Copy each login only into its own root-protected service environment; never place either login in the application secret or general host environment.

## Release flow

After merge, CI and the separate infrastructure decision, use the repository release command for the approved semantic version and issue list. Publication and deployment require their own approval. The release agent verifies the signed tag, commit, image provenance, manifest, ordered migration inventory and expected inactive gates before it runs the candidate.

The release contract retains the v1 receipt fields for compatibility. `privacy_worker_active` and `privacy_worker_activation_required` must both be `false`; any other value fails verification. Guardian intake remains independently fail closed until its own activation procedure is approved.

If a candidate fails before traffic switching, the active service is unchanged. A post-switch failure restores the previous route and environment and quarantines the failed digest. Releasing a replacement digest is the normal recovery path.

Useful status commands:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env -f /opt/mycfc/deployment/compose.yaml ps
sudo systemctl status mycfc-pull-release.timer
sudo systemctl status mycfc-media-cleanup.service
sudo journalctl -u mycfc-pull-release.service -n 100 --no-pager
sudo /opt/mycfc/deployment/release-status.sh
sudo /opt/mycfc/deployment/release-status.sh --json
```

## Database maintenance

The release runs bootstrap, forward-only migration and privilege hardening before candidate startup. Migration `202609220001_privacy_automation_retirement` is permanent: releases must not restore privacy intake, automation roles, services or schedules.

Media cleanup uses its own database login and only the `media_upload_cleanup_*` API. Manual retention is available as a one-shot binary in the signed image but has no service or timer; an authorized operator runs the `manual-maintenance` Compose profile only after a request-specific review and captures aggregate evidence. See `docs/privacy-rights-operations.md`.

Guardian-authority activation remains a separate root-only operation with isolated `mycfc_guardian_activation_operator` and `mycfc_guardian_release_bind` logins. Routine releases do not provision approval evidence or enable guardian intake. See `docs/guardian-authority-operations.md`.

## Backups and recovery

The nightly backup service writes encrypted, authenticated PostgreSQL backups using the dedicated `mycfc-backup` profile. The standing profile cannot delete versions. Non-current cleanup requires a separately provisioned short-lived session and the independent cleaner gates.

Before restoring production, stop the release poller, select and verify an exact backup version, restore into an isolated database, apply current migrations, run the full verification suite and obtain explicit approval for the production change. A restored database is never allowed to undo the privacy-automation retirement migration.

# Single-host deployment

This directory runs MyCFC on one Hetzner host: Cloudflare Tunnel is the public edge, Caddy and PostgreSQL have no host ports, and a systemd timer pulls approved immutable ECR releases.

Cloudflare Tunnel connects outbound to Cloudflare and proxies to Caddy over the private Docker network. Caddy trusts client-IP headers only from private Docker ranges. Keep the Hetzner firewall closed to public web traffic.

## Host setup

1. Clone this repository at `/opt/mycfc` on the host. The one-off migration container applies the embedded baseline when PostgreSQL is empty.
2. Install Docker Engine with the Compose plugin. Do not permit inbound TCP 80 or 443.
3. Install AWS CLI v2, `curl`, `jq`, and Docker Engine with the Compose plugin. Configure an AWS identity that can only read the production ECR repository.
4. Create `/etc/mycfc/mycfc.env` as root, then set its mode to `0600`. This file is deliberately untracked and must never be copied into the repository.
5. Run `sudo sh deployment/install.sh` from this checkout.

The installer validates the Compose configuration, installs and enables the pull-release systemd timer, then starts an immediate release check. It refuses an environment file that is not `root:root` mode `0600`.

## Required environment

`/etc/mycfc/mycfc.env` must define the following values. Use real production values, not the local `.env.example` values.

```text
MYCFC_IMAGE=<account>.dkr.ecr.<region>.amazonaws.com/mycfc-app@sha256:<immutable-digest>
ECR_REPOSITORY_URL=<account>.dkr.ecr.<region>.amazonaws.com/mycfc-production
CLOUDFLARE_TUNNEL_TOKEN=<Cloudflare remotely-managed tunnel token>
MYCFC_DOMAIN=example.com
APP_VERSION=<release-version>
GIT_SHA=<40-lowercase-hex-commit>
BASE_URL=https://example.com
EMAIL_VERIFICATION_HMAC_KEY_B64=<email_verification_hmac_key_b64 Terraform output>
SMTP_HOST=email-smtp.eu-west-1.amazonaws.com
SMTP_PORT=587
SMTP_USERNAME=<ses_smtp_username Terraform output>
SMTP_PASSWORD=<ses_smtp_password Terraform output>
SMTP_FROM_ADDRESS=no-reply@mycfcoimbra.com
SMTP_FROM_NAME=MyCFC
SMTP_TLS_MODE=starttls
SMTP_TIMEOUT=10s

POSTGRES_DB=<database-name>
POSTGRES_USER=<bootstrap-superuser>
POSTGRES_PASSWORD=<bootstrap-superuser-password>
APP_DB_USER=<restricted-application-user>
APP_DB_PASSWORD=<restricted-application-password>
MIGRATION_DB_USER=<schema-migration-user>
MIGRATION_DB_PASSWORD=<schema-migration-password>
CSRF_AUTH_KEY_B64=<base64-encoded-32-byte-key>

AWS_REGION=<aws-region>
AWS_ACCESS_KEY_ID=<aws-access-key-id>
AWS_SECRET_ACCESS_KEY=<aws-secret-access-key>
# AWS_SESSION_TOKEN=<optional-session-token>
S3_BUCKET_NAME=<private-s3-bucket>

# Root-only backup identity, stored separately from these application credentials.
BACKUP_S3_BUCKET=<private-postgresql-backup-bucket>
BACKUP_KMS_KEY_ID=<KMS-key-ARN-or-alias>

GOOGLE_CALENDAR_API_KEY=<google-api-key>
CALENDAR_COMPETITION_ID=<calendar-id>
CALENDAR_TRAINING_ID=<calendar-id>
CALENDAR_SOCIAL_ID=<calendar-id>
CALENDAR_CLEANUPS_ID=<calendar-id>
GALLERY_URL=https://example.com/gallery

CONSENT_TERMS_VERSION=<version>
CONSENT_TERMS_SHA256=<64-lowercase-hex-sha256>
CONSENT_TERMS_URL=https://example.com/legal/termos-gerais
CONSENT_IMAGE_VERSION=<version>
CONSENT_IMAGE_SHA256=<64-lowercase-hex-sha256>
CONSENT_IMAGE_URL=https://example.com/legal/uso-imagem
CONSENT_MINOR_VERSION=<version>
CONSENT_MINOR_SHA256=<64-lowercase-hex-sha256>
CONSENT_MINOR_URL=https://example.com/legal/responsabilidade-menor
```

The long-running application receives only the restricted `APP_DB_*` credential. The release agent uses the bootstrap credential in a one-off role-provisioning container and then uses only the `MIGRATION_DB_*` credential for the one-off migration container. Neither privileged credential is present in the application container. Database URLs use `sslmode=disable` because PostgreSQL traffic never leaves the private Docker network. After CI, GitHub publishes an immutable `release-<UTC>-<SHA>` ECR tag. The timer selects the latest valid release tag through ECR's authenticated registry API, resolves its digest locally, then updates `MYCFC_IMAGE`, `APP_VERSION`, and `GIT_SHA` atomically. It checks readiness, login, and the fingerprinted JavaScript asset through private Caddy using the production virtual host; a failure restores the prior release. Public Cloudflare checks must originate outside the Hetzner host.

The retained AWS Terraform stack provisions the SES identity, authoritative Cloudflare DKIM and MAIL FROM records, least-privilege SMTP credentials, and the email-verification HMAC key. Install its sensitive outputs in this environment file before releasing, confirm SES production access, and run `sudo ./deployment/verify-ses.sh`. The smoke test sends only to the AWS SES mailbox simulator.

The ECR repository retains immutable `git-<SHA>` and `release-<UTC>-<SHA>` tags. The host identity needs `ecr:GetAuthorizationToken`, `ecr:DescribeImages`, `ecr:BatchGetImage`, `ecr:BatchCheckLayerAvailability`, and `ecr:GetDownloadUrlForLayer`; it must not have image push or delete permissions. The agent uses `ecr:DescribeImages` to select the release and verify its digest after pulling it. Use a separate read-only ECR credential from the application's S3 credential when the host's credential provisioning is updated.

Create `/etc/mycfc/backup-aws/credentials` as `root:root` mode `0600` before running the installer. It must contain the dedicated `mycfc-backup` profile used only for the backup bucket and KMS key:

```text
[mycfc-backup]
aws_access_key_id=<backup-access-key-id>
aws_secret_access_key=<backup-secret-access-key>
```

The installer refuses to proceed until this credential file and `BACKUP_S3_BUCKET` and `BACKUP_KMS_KEY_ID` are present, then enables both the release-poll and nightly backup timers.

## Operations

Run all commands with the protected environment file:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml ps
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml logs -f
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml up -d --pull always
sudo systemctl status mycfc-pull-release.timer
sudo journalctl -u mycfc-pull-release.service -n 100 --no-pager
```

Persistent named volumes retain PostgreSQL data and Caddy certificates/configuration. Do not remove `pgdata` without a verified backup. Before replacing the application container, the release agent idempotently provisions/rotates the restricted roles, transfers legacy bootstrap-owned schema objects to the migration role, grants runtime DML privileges, and runs the new image's `migrate` command as the migration role. On an empty volume that command applies `internal/db/schema.sql`; on an existing database it records and applies pending forward-only migrations from `internal/db/migrations`. The web process never runs migrations during startup. A bootstrap or migration failure aborts the rollout before the application is replaced.

## Release rollback and incident access

The release agent saves the last known-good environment as `/etc/mycfc/mycfc.env.previous` before every rollout. A failed rollout restores it automatically. To hold a manual rollback while investigating a bad release, stop the polling timer before restoring it:

```sh
sudo systemctl stop mycfc-pull-release.timer
sudo cp /etc/mycfc/mycfc.env.previous /etc/mycfc/mycfc.env
sudo docker compose --env-file /etc/mycfc/mycfc.env -f /opt/mycfc/deployment/compose.yaml up -d --wait --force-recreate app
```

Check `/health/ready`, `/login`, and the fingerprinted browser asset through Cloudflare from an external network. Leave the timer stopped until a replacement release is available; restarting it immediately promotes the newest ECR release again. Restore normal polling with `sudo systemctl start mycfc-pull-release.timer`.

For a host incident, use the separate operator SSH key from an approved SSH CIDR. The deploy key is limited to deployment automation. Keep the Cloudflare Tunnel public hostname enabled during application rollback. Revert the public hostname to the retained AWS origin only during the approved rollback window, validate the same external checks, and do not retire AWS resources until that path and the PostgreSQL restore drill are accepted.

## PostgreSQL recovery

`mycfc-postgres-backup.timer` runs nightly at 02:15 UTC. It creates a custom-format `pg_dump`, encrypts it locally with a KMS-generated data key, and uploads the encrypted dump and its envelope metadata to the private backup bucket. Daily recovery points expire after 30 days; a second copy is retained monthly for 365 days. S3 SSE-KMS is an additional storage-at-rest control. Hetzner server backups are a separate recovery path, not a substitute for logical dumps.

The recovery-point objective is 24 hours. The recovery-time objective is four hours, including replacement-host provisioning, credential recovery, download/decryption, restore, and application checks.

Run a non-destructive restore drill with:

```sh
sudo /opt/mycfc/deployment/postgres-restore-drill.sh
```

The drill downloads the newest daily recovery point, verifies its ciphertext checksum, decrypts the envelope key through KMS, restores into an isolated temporary PostgreSQL container with `--no-owner`, verifies the application `users` table, and removes the container and temporary files. Run it after any backup-script, PostgreSQL-major-version, KMS-policy, or credential change, and at least quarterly. Record its date, recovery-point timestamp, duration, and result in the #43 issue.

Check backup status with `sudo systemctl status mycfc-postgres-backup.service` and `sudo journalctl -u mycfc-postgres-backup.service -n 100 --no-pager`. Investigate any failed run before the next backup window; confirm free disk space before retrying. Rotate the `mycfc-production-postgres-backups` IAM access key by creating its replacement, atomically replacing `/etc/mycfc/backup-aws/credentials` as root mode `0600`, running a backup and restore drill, then disabling and deleting the previous key. Review Docker, PostgreSQL, Caddy, and application releases monthly; apply Ubuntu security updates automatically and schedule PostgreSQL major-version upgrades with a tested restore path.

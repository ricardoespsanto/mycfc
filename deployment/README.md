# Single-host deployment

This directory runs MyCFC on one Hetzner host: Caddy is the only public service, PostgreSQL has no host port, and a systemd timer pulls approved ECR releases. Caddy obtains and renews TLS certificates for `MYCFC_DOMAIN`.

## Host setup

1. Clone this repository at `/opt/mycfc` on the host. The Compose file mounts `internal/db/schema.sql` to initialize a new PostgreSQL volume.
2. Install Docker Engine with the Compose plugin, allow inbound TCP 80 and 443, and point DNS for the production domain at the host.
3. Install AWS CLI v2, `curl`, `jq`, and Docker Engine with the Compose plugin. Configure an AWS identity that can only read the production ECR repository.
4. Create `/etc/mycfc/mycfc.env` as root, then set its mode to `0600`. This file is deliberately untracked and must never be copied into the repository.
5. Run `sudo sh deployment/install.sh` from this checkout.

The installer validates the Compose configuration, installs and enables the pull-release systemd timer, then starts an immediate release check. It refuses an environment file that is not `root:root` mode `0600`.

## Required environment

`/etc/mycfc/mycfc.env` must define the following values. Use real production values, not the local `.env.example` values.

```text
MYCFC_IMAGE=<account>.dkr.ecr.<region>.amazonaws.com/mycfc-app@sha256:<immutable-digest>
ECR_REPOSITORY_URL=<account>.dkr.ecr.<region>.amazonaws.com/mycfc-production
MYCFC_DOMAIN=example.com
APP_VERSION=<release-version>
GIT_SHA=<40-lowercase-hex-commit>
BASE_URL=https://example.com

POSTGRES_DB=<database-name>
POSTGRES_USER=<database-user>
POSTGRES_PASSWORD=<database-password>
CSRF_AUTH_KEY_B64=<base64-encoded-32-byte-key>

AWS_REGION=<aws-region>
AWS_ACCESS_KEY_ID=<aws-access-key-id>
AWS_SECRET_ACCESS_KEY=<aws-secret-access-key>
# AWS_SESSION_TOKEN=<optional-session-token>
S3_BUCKET_NAME=<private-s3-bucket>

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

The Compose bundle constructs `DATABASE_URL` from the PostgreSQL variables. It uses `sslmode=disable` because PostgreSQL traffic never leaves the private Docker network. The timer resolves the mutable ECR `production` tag to an immutable digest, then updates `MYCFC_IMAGE`, `APP_VERSION`, and `GIT_SHA` atomically. It checks readiness, login, and the fingerprinted JavaScript asset through local Caddy; a failure restores the prior release.

The ECR repository must allow the `production` tag to move while retaining immutable `git-<SHA>` tags. The host identity needs `ecr:GetAuthorizationToken`, `ecr:BatchGetImage`, `ecr:BatchCheckLayerAvailability`, and `ecr:GetDownloadUrlForLayer`; it must not have image push or delete permissions. The agent reads the resolved digest and revision label from the pulled local image, so it does not need `ecr:DescribeImages`. Use a separate read-only ECR credential from the application's S3 credential when the host's credential provisioning is updated.

## Operations

Run all commands with the protected environment file:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml ps
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml logs -f
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml up -d --pull always
sudo systemctl status mycfc-pull-release.timer
sudo journalctl -u mycfc-pull-release.service -n 100 --no-pager
```

Persistent named volumes retain PostgreSQL data and Caddy certificates/configuration. Do not remove `pgdata` without a verified backup. The schema is mounted from `internal/db/schema.sql` and applied only when PostgreSQL initializes an empty volume; this bundle does not provide an in-place database migration path.

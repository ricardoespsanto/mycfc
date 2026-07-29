# Single-host deployment

This directory runs MyCFC on one Hetzner host: Caddy is the only public service, the application is pulled from ECR, and PostgreSQL has no host port. Caddy obtains and renews TLS certificates for `MYCFC_DOMAIN`.

## Host setup

1. Clone this repository on the host. The Compose file mounts `internal/db/schema.sql` to initialize a new PostgreSQL volume.
2. Install Docker Engine with the Compose plugin, allow inbound TCP 80 and 443, and point DNS for the production domain at the host.
3. Authenticate Docker to the ECR registry before each pull, for example with an AWS identity allowed to read the repository.
4. Create `/etc/mycfc/mycfc.env` as root, then set its mode to `0600`. This file is deliberately untracked and must never be copied into the repository.
5. Run `sudo sh deployment/install.sh` from this checkout.

The installer validates the Compose configuration, pulls the image, and starts the services. It waits for application readiness through Caddy and refuses an environment file that is not `root:root` mode `0600`.

## Required environment

`/etc/mycfc/mycfc.env` must define the following values. Use real production values, not the local `.env.example` values.

```text
MYCFC_IMAGE=<account>.dkr.ecr.<region>.amazonaws.com/mycfc-app@sha256:<immutable-digest>
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

The Compose bundle constructs `DATABASE_URL` from the PostgreSQL variables. It uses `sslmode=disable` because PostgreSQL traffic never leaves the private Docker network. The ECR image receives the standard AWS credential variables because it accesses the production S3 bucket directly.

## Operations

Run all commands with the protected environment file:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml ps
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml logs -f
sudo docker compose --env-file /etc/mycfc/mycfc.env -f deployment/compose.yaml up -d --pull always
```

Persistent named volumes retain PostgreSQL data and Caddy certificates/configuration. Do not remove `pgdata` without a verified backup. The schema is mounted from `internal/db/schema.sql` and applied only when PostgreSQL initializes an empty volume; this bundle does not provide an in-place database migration path.

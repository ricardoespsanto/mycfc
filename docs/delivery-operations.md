# Verified delivery operations

MyCFC releases are tag-first. A green commit on `main` is necessary but does not publish or deploy by itself. The production human gate is the GitHub Environment approval on `deploy.yml`. Feature activation (guardian intake, etc.) remains a separate operator procedure.

## Simplified release flow

1. Merge the change to `main` and wait for CI to go green.
2. From a clean, up-to-date `main` tip:

```sh
make release VERSION=vX.Y.Z
# optional: ISSUES=123,456 attaches delivery comments after success
```

3. Approve the **production** GitHub Environment when the workflow asks.
4. Re-run the same `make release VERSION=vX.Y.Z` command to confirm `state=delivered` once the host has pulled and switched traffic.

`scripts/release.sh` will:

- refuse anything that is not clean `main` matching `origin/main`
- require a valid signed commit and a successful CI push run for that SHA
- create and push a signed annotated semantic tag when missing
- dispatch `deploy.yml` for the exact SHA and version
- avoid duplicate dispatches when an identical run is already in progress
- verify publication + deployment-receipt assets when the GitHub release exists

There are no `RELEASE_MERGE_APPROVED`, `RELEASE_INFRA_APPLIED`, `RELEASE_PUBLISH_DEPLOY_APPROVED`, or `RELEASE_DEPLOY_RETRY_APPROVED` environment variables. Infrastructure changes use the existing Terraform plan/apply workflows on their own PRs; an application release does not re-litigate Terraform.

## What deploy.yml still verifies

The protected workflow still checks:

- signed annotated tag and signed commit on `main`
- successful canonical CI push run for that SHA
- schema/migration inventory and image labels
- image and manifest attestation
- host pull receipt via the operations observer
- public `/health/ready` (Cloudflare challenges are accepted as edge proof)

The host remains pull-based blue/green: migrate → candidate checks → traffic switch, with automatic rollback on post-switch failure. Privacy-worker and guardian-intake fields in the receipt stay permanently `false` for routine releases.

## Verification and diagnosis

Every pull request and every push to `main` still runs `make test-release-upgrade`. Release tooling tests live under `make test-release-tooling`.

On the host:

```sh
sudo /opt/mycfc/deployment/release-status.sh
sudo /opt/mycfc/deployment/release-status.sh --json
sudo journalctl -u mycfc-pull-release.service -n 100 --no-pager
```

## Infrastructure and activation

Terraform apply stays on the dedicated protected workflows. Deployment does not authorize privacy, guardian-intake, retention, purge, or other activation decisions — use the feature-specific operator procedures for those.

Optional decision records:

```sh
make approval-packet APPROVAL_KIND=release OUTPUT=/tmp/release-approval.md
```

## Rollback

Stop the polling timer before a deliberate manual rollback. Pre-switch failures leave the active route untouched; post-switch failures restore the previous upstream and quarantine the failed digest.

# Verified delivery operations

MyCFC releases are tag- and dispatch-first. A green commit on `main` is necessary but does not publish or deploy anything by itself. Merge, infrastructure apply, release publication/deployment, and feature activation remain separate human approval gates.

## Release contract

Run `make release VERSION=vX.Y.Z ISSUES=123,456` from the release branch. `scripts/release.sh` stores and validates resumable, non-secret state below `.git`, then stops at each boundary that still needs a human decision. `RELEASE_MERGE_APPROVED=true` authorizes only the named, green pull request merge; it never applies Terraform or infers a later approval.

After the reviewed commit is merged and CI is green, set `RELEASE_INFRA_APPLIED=true` only when a separately reviewed infrastructure plan has actually been applied or infrastructure is unchanged. Set `RELEASE_PUBLISH_DEPLOY_APPROVED=true` only for the exact version and commit named in the release approval. The script creates and locally verifies a signed annotated semantic tag, pushes that tag, and dispatches `deploy.yml` for the exact main-branch SHA. Re-running the same command resumes from durable state and remote evidence. It detects an exact queued or running deployment instead of dispatching another one. A failed exact run stops at a separate retry gate; only `RELEASE_DEPLOY_RETRY_APPROVED=true` authorizes that retry.

The protected workflow independently verifies the tag object, signed commit, main ancestry, and the canonical `.github/workflows/ci.yml` push run. It verifies the application image provenance, creates a canonical manifest with the tested tree, ordered migrations and expected gate states, packages and attests that manifest as an immutable registry artifact, and only then publishes the deterministic `release-<tag-time>-<SHA>` pickup tag. The host remains pull-based and receives no GitHub write credential. Before any candidate command runs, it verifies both registry subjects against the exact repository, deploy workflow and source SHA, extracts the signed manifest, and matches it to the image labels and release tag.

Two canonical, sorted JSON contracts bind the handoff:

- `mycfc/release-publication/v1` binds semantic version, commit and tree, image repository/digest, release tag, the application's exact ordered database inventory and matching digest, expected gates, CI run, stable publication time, and explicit issue IDs.
- `mycfc/deployment-receipt/v1` binds the manifest digest and release identity to host result, slot, failure phase, traffic-switch/rollback outcomes, observed guardian-intake and privacy-worker states, any outstanding privacy-worker activation requirement, and start/finish times.

The host writes its manifest and receipt atomically below `/etc/mycfc/deployment` and sends only the allowlisted receipt summary to `/mycfc/production/deployment`. Already-current runs emit a fresh exact receipt. GitHub re-assumes the short-lived operations-observer role after publication and accepts only a receipt created after that publication attempt and within the bounded freshness window. It also checks observed gate states against the signed policy; a staged inactive privacy worker is accepted only when the receipt explicitly says activation is still required. A separate job with no AWS identity then records the immutable publication plus an append-only, timestamp-and-hash-named receipt on the GitHub release and adds a new hash-bound comment only to explicitly supplied issue IDs.

The release login can read only the guardian-intake boolean for the exact database, image, and schema identity being receipted. The database returns an unknown result for an absent or stale binding, which stops both an already-current receipt and a newly deployed receipt; the release login cannot call the broader guardian status or activation APIs.

## Verification and diagnosis

Every pull request and every push to `main`, including documentation-only changes, runs the predecessor-to-candidate `make test-release-upgrade` gate. The gate builds both revisions, creates a predecessor database, executes production-order bootstrap/migration/hardening/guardian binding with the candidate, then checks candidate liveness, readiness, login HTML, and its fingerprinted JavaScript asset. `make test-release-tooling` and `deployment/pull-release_test.sh` separately prove resumability, immutable evidence, and that a failing candidate never changes the active route while rollback/quarantine state is retained.

On the host, use:

```sh
sudo /opt/mycfc/deployment/release-status.sh
sudo /opt/mycfc/deployment/release-status.sh --json
sudo journalctl -u mycfc-pull-release.service -n 100 --no-pager
```

The status output contains operational identifiers and service states only. It never prints environment or credential-file contents. The JSON form is intended for local operator tooling; the CloudWatch verifier accepts only the fixed receipt fields and rejects credential-shaped text.

## Observer boundary

`operations_observer_enabled` is false by default. When explicitly planned and applied, Terraform creates a role with a one-hour maximum session and no IAM user or access key. Human trust is limited to exact IAM Identity Center permission-set role ARNs; MFA must be enforced in Identity Center. Workflow trust is limited to the exact GitHub repository and protected production-environment OIDC subject. Its permissions boundary and inline policy allow only deployment-log, ECR inventory, and alarm reads, while explicitly denying secret, Parameter Store, Terraform-state S3, ECR/log mutation, and role chaining.

## Rollback and activation

Migration, guardian release binding, candidate checks, traffic switch, and post-switch verification retain the existing ordered release phases. Any pre-switch failure leaves the active route untouched; any post-switch failure restores the previous upstream and environment and quarantines the failed digest. Stop the polling timer before a deliberate manual rollback.

Deployment does not authorize a privacy, guardian-intake, retention, purge, or other activation decision. Use the feature-specific operator procedure for those actions. `make approval-packet APPROVAL_KIND=merge|release|infrastructure|credential|purge|activation OUTPUT=<path>` creates a short, single-gate decision record; it rejects multiline and credential-shaped inputs. The five operation issue forms separate infrastructure/identity, data work, approval evidence, activation, and observation/closeout.

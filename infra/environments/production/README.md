# Retained AWS and runtime-config Terraform

The ECS/RDS/ALB runtime was retired. This module manages the private repair-photo bucket, immutable ECR repository, Amazon SES transactional-email identity, production runtime configuration in SSM Parameter Store and Secrets Manager, deployment alerts and logs, and separate application-runtime and release-agent IAM users for the Hetzner host. PostgreSQL backup storage and the Hetzner server resource are managed by `../hetzner`.

Use the existing remote backend and run `terraform plan` before applying a retained-resource change. Do not restore retired runtime resources from this module. Normal retained-stack delivery uses the approval-gated GitHub workflows described below; local apply is reserved for one-time OIDC bootstrap or recovery.

## GitHub Terraform delivery

The credential-free `Infrastructure` workflow runs formatting, validation, tests, and linting for pull requests and `main`. Pull-request code never receives production state, secrets, or an AWS identity. After merge and successful canonical CI, `Terraform production plan` can be manually dispatched from `main` with its exact current signed SHA for an informational preview. The protected `production-plan` job rechecks that identity and assumes the read-only `github-infra-plan` role. Its job summary reports only action counts, resource addresses, normal-apply eligibility, and a fingerprint of the redacted plan; Terraform's redacted plan remains in the protected job log for diagnosis. Plan files, JSON, and variable files are never uploaded.

Production application is never triggered by a push. Manually dispatch `Terraform production apply` from `main` with the exact current `main` SHA. Its first job revalidates the SHA against the dispatch, checkout, current remote branch, GitHub signature, and canonical CI; runs the full credential-free Terraform gate; then creates and displays the protected plan. Only after that job finishes does the dependent `production` environment request approval. The apply job regenerates the plan, requires both its redacted human-review fingerprint and a keyed HMAC over the complete unredacted JSON semantics to match, rechecks current `main`, applies the saved binary it just validated, verifies a no-change post-apply plan, and removes all ephemeral files. The separate preview workflow is not a substitute for this approval-bound plan.

Before enabling these jobs, configure `production-plan` and `production` with required reviewers, administrator bypass disabled, and protected-branch deployment rules. Configure:

- `production-plan` variables `AWS_REGION`, `TF_BACKEND_BUCKET`, and `AWS_INFRA_PLAN_ROLE_ARN`, plus secrets `TF_VARS`, `CLOUDFLARE_API_TOKEN`, and `TF_PLAN_HMAC_KEY`; this environment's Cloudflare token must be a distinct zone/DNS read-only token, never the apply token;
- `production` variables `AWS_REGION`, `TF_BACKEND_BUCKET`, and `AWS_INFRA_APPLY_ROLE_ARN`, plus secrets `TF_VARS`, `CLOUDFLARE_API_TOKEN`, and `TF_PLAN_HMAC_KEY`;
- GitHub OIDC trust restricted to audience `sts.amazonaws.com` and the exact environment subjects `repo:ricardoespsanto/mycfc:environment:production-plan` or `repo:ricardoespsanto/mycfc:environment:production` as appropriate;
- a plan role limited to resource reads, required bucket metadata/list access, `GetObject` on the exact state object, and `GetObject`/`PutObject`/`DeleteObject` only on the exact lock object; explicitly deny state-object writes and deletes;
- an apply role limited to retained-stack changes plus the exact state object and lock.

Set `TF_PLAN_HMAC_KEY` in both environments to the same independently generated 32-byte lowercase hexadecimal key. It binds sensitive plan values and the backend bucket, fixed `mycfc/production/terraform.tfstate` key, and region without publishing a guessable bare digest; a missing, malformed, or mismatched input fails closed. The calculation excludes the nondeterministic plan timestamp and normalizes only the expected ARN/user-ID difference between `github-infra-plan` and `github-infra-apply` for `data.aws_caller_identity.current`; it still binds the AWS account ID and every other plan field. Keep the HMAC key separate from application secrets and rotate both copies together only when no apply run is awaiting approval.

The two roles belong in a separate, manually controlled bootstrap boundary so this production root cannot alter its own apply authority. Never reuse the image-publishing role for Terraform, and never reuse the apply Cloudflare token for planning. The normal apply workflow accepts only complete Terraform JSON format 1.2 plans containing no delete, replacement, forget, import, action invocation, deferred change, unknown action, or provisioner. Those mechanically blocked operations require their own narrowly scoped maintenance procedure rather than a flag on the normal apply workflow. Allowed creates and in-place updates can still change access or behavior, so the protected-environment reviewer must assess their semantics.

The legacy-media purge identity is a temporary, independently gated exception documented in `docs/legacy-media-purge-operations.md`. Its inventory and deletion permissions are separate, expire at an exact future UTC deadline no more than 24 hours after plan time, and are capped by an identical permissions boundary. Deletion is ordered after a separately gated, equally expiring bucket deny that fences new object versions and delete markers under the three purge prefixes; teardown removes deletion first. Terraform creates no access key. Leave all purge inputs at their inert defaults outside the approved one-time maintenance window.

The optional privacy-worker source is disabled by default and creates no credentials, secret value, service, timer, alarm, or activation. Its three separate infrastructure, version-deletion, and metadata-rewrite gates and rollback procedure are documented in [`../../../docs/privacy-worker-infrastructure.md`](../../../docs/privacy-worker-infrastructure.md). Source delivery does not authorize a Terraform apply or live erasure.

The optional operations observer is also disabled by default. It creates no access key or IAM user. When separately reviewed and applied, `operations_observer_enabled` creates a one-hour role trusted only by exact IAM Identity Center permission-set role ARNs (with MFA enforced in Identity Center) and/or the exact protected GitHub production-environment OIDC subject. A matching permissions boundary and inline policy allow deployment-log, ECR inventory, and alarm reads while denying secret, Parameter Store, state-bucket, ECR/log mutation, and role-chaining access. Configure the resulting role ARN as the protected `AWS_OPERATIONS_OBSERVER_ROLE_ARN` repository environment variable; never install it on the Hetzner host.

## Amazon SES provisioning

SES is provisioned in `eu-west-1` for the production `domain_name`. Terraform creates:

- an SESv2 domain identity using 2048-bit Easy DKIM;
- the three DKIM CNAMEs in the authoritative Cloudflare zone;
- a strict custom `mail.<domain>` MAIL FROM domain with MX and SPF records;
- account suppression for hard bounces and complaints;
- one IAM user restricted to `ses:SendRawEmail` from `no-reply@<domain>`;
- an SMTP access key and a separate 32-byte email-verification HMAC key.

The Cloudflare provider discovers the active zone by `domain_name`. Before planning, authenticate both providers without storing credentials in Terraform files:

```sh
aws login
export CLOUDFLARE_API_TOKEN=<token-with-zone-read-and-dns-write>
terraform init -backend-config=backend.hcl
terraform plan -var-file=terraform.tfvars
```

Review the plan, then apply it. Terraform writes application runtime values into SSM under `/mycfc/production` and writes the application secret JSON into Secrets Manager as `/mycfc/production/app-secrets`. The secret value is sensitive and stored in the encrypted remote Terraform state, so keep backend access tightly scoped.

```text
runtime_parameter_prefix = /mycfc/production
runtime_secret_arn       = arn:aws:secretsmanager:...
```

Install the sensitive `host_runtime_access_key_id` and `host_runtime_secret_access_key` outputs in `/etc/mycfc/mycfc.env` as `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`. This application identity can read the production SSM parameters and application secret and use the repair-photo bucket; it cannot access ECR or deployment logs.

Install `release_agent_access_key_id` and `release_agent_secret_access_key` as the `mycfc-release` profile in `/etc/mycfc/release-aws/credentials`. This release identity can only read the production ECR repository and write the deployment log group. Set `alarm_email` before applying Terraform and confirm the resulting SNS subscription email. The alarm enters ALARM after failures occur in at least two of three consecutive five-minute periods.

Keep `release_agent_cutover_complete = false` during initial provisioning so the existing host identity retains ECR and deployment-log access. After installing the dedicated profile and updated scripts, run `release-status.sh` and one successful release check, then set the variable to `true` and apply again. This expand/contract sequence prevents a credential rollout from interrupting release polling.

Easy DKIM verification can take several minutes after DNS publication. Confirm both the identity and account before releasing:

```sh
aws sesv2 get-email-identity --region eu-west-1 --email-identity mycfcoimbra.com \
  --query '{Verified:VerifiedForSendingStatus,Dkim:DkimAttributes.Status}'
aws sesv2 get-account --region eu-west-1 \
  --query '{SendingEnabled:SendingEnabled,ProductionAccessEnabled:ProductionAccessEnabled,EnforcementStatus:EnforcementStatus}'
```

New SES accounts are region-specific and may remain in the sandbox. Terraform cannot approve production access; request it through SES before release if `ProductionAccessEnabled` is false. The [AWS production-access procedure](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html) describes the review.

After applying Terraform, run `sudo ./verify-ses.sh` on the host. It performs a STARTTLS-authenticated SMTP delivery to the AWS SES success simulator and does not send mail to a real recipient.

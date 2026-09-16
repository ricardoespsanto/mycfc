# Privacy activation through GitHub Actions

Issue #274 is delivered in stages. This first stage prepares the existing protected Terraform workflows to provision the worker identity and restore infrastructure. It does not deploy the application, provision credentials, enable erasure, stop the service, reset the database, purge media, or activate the worker. All live operations use GitHub Actions; local validation uses no cloud or host credentials.

## Reviewed desired infrastructure

Both manual Terraform workflows accept `stack`, an allowlisted choice of `production` (the default) or `hetzner`. They retain their existing filenames: `terraform-production-plan.yml` and `terraform-production-apply.yml`. Dispatch from `main` with the exact current, signature-verified, CI-green commit. The read-only preview uses the `production-plan` environment. Apply first builds the plan there, then waits for approval in the protected `production` environment before rebuilding and checking the exact human-review fingerprint and keyed semantic digest. A changed main commit, changed plan, deletion/replacement, unsupported action, or post-apply drift fails closed.

`scripts/terraform-stack.sh` fixes the root and backend key rather than accepting paths:

| Stack | Root | State key | Selected inputs |
| --- | --- | --- | --- |
| `production` | `infra/environments/production` | `mycfc/production/terraform.tfstate` | `TF_VARS`, `CLOUDFLARE_API_TOKEN` |
| `hetzner` | `infra/environments/hetzner` | `mycfc/hetzner/terraform.tfstate` | `HETZNER_TF_VARS`, `HCLOUD_TOKEN` |

Each root's checked-in `privacy-infrastructure.tfvars` is loaded explicitly **after** secret `ci.auto.tfvars` on every preview, pre-approval plan, apply reconstruction and post-apply drift check. The files are permanent desired configuration, not a one-time dispatch override. Subsequent workflow runs retain the resources. The production file enables only `privacy_worker_infrastructure_enabled`; the Hetzner file enables only `privacy_restore_infrastructure_enabled`. The files explicitly keep worker deletion/invocation/monitoring, restore write/replay, legacy purge and backup cleanup gates disabled. Later stages must update these source files through review; setting a conflicting secret value cannot bypass them. Mocked Terraform tests retain the source's inert defaults because these files are not auto-loaded.

The restore infrastructure includes its encrypted, versioned Object Lock bucket, KMS key, fixed broker and isolated identities. Its compliance retention and costs must be reviewed in the concrete plan before apply. The new `privacy_restore_ledger_broker_function_arn` output supplies the exact ARN for a later invocation-permission stage. No access keys or secret values are created by these gates. Plan/state JSON and secret variable files are not uploaded as artifacts; the runner deletes the generated files on exit.

## GitHub prerequisites

Do not assume the following are provisioned merely because source supports them. Configure and review them through the approved GitHub administration/bootstrap process before dispatch; this workflow does not bootstrap its own authority.

- Both protected environments retain `AWS_REGION`, `TF_BACKEND_BUCKET`, their respective `AWS_INFRA_PLAN_ROLE_ARN` / `AWS_INFRA_APPLY_ROLE_ARN`, and the identical 64-character hexadecimal `TF_PLAN_HMAC_KEY`. The role trust must remain restricted to this repository and the corresponding protected environment, with OIDC rather than standing AWS keys.
- `production-plan` and `production` each need `HETZNER_TF_VARS` containing the existing Hetzner root configuration (including the exact existing SSH public keys and source-IP list), and `HCLOUD_TOKEN` for the correct existing project. The planning environment uses a read-only Hetzner token; the applying environment uses a separately protected write-capable token. Do not replace the existing production `TF_VARS` / `CLOUDFLARE_API_TOKEN`.
- Extend the two external AWS roles' exact backend scope to `arn:aws:s3:::STATE_BUCKET/mycfc/hetzner/terraform.tfstate` and `arn:aws:s3:::STATE_BUCKET/mycfc/hetzner/terraform.tfstate.tflock`, preserving existing production access. Both need scoped `s3:ListBucket` plus `s3:GetObject` for state; planning and applying need `GetObject`/`PutObject`/`DeleteObject` on the lock object; only apply needs `PutObject` on state. Preserve any existing backend encryption-key requirements. Do not grant broad access to unrelated state or secret objects.
- The plan role needs read permissions for the existing Hetzner root's AWS resources and proposed ledger IAM/KMS/S3/Lambda/CloudWatch resources. Apply needs the reviewed resource-scoped creation/update permissions for that root, including exact broker-role `iam:PassRole` to Lambda. These roles are externally provisioned: Terraform does not silently broaden them. Existing production-root authority alone is insufficient.
- Keep the existing `github-infra-plan` and `github-infra-apply` role names used by semantic normalization, and the same reviewed variables in both environments. The backend bucket, selected state key and AWS region are bound into the HMAC. Plan and apply must operate in the same account and region.

Use the Actions UI to run **Terraform production plan**, selecting one stack and the current main SHA. Review its resource actions before dispatching **Terraform production apply** for that stack and SHA. Review the new plan before approving its production job. Repeat for the other stack. No automatic apply or local AWS/SSH fallback is provided.

## Discover existing AWS inputs without reading their values

The preview workflow additionally offers `operation=discover-inputs` (`plan` remains the default). It retains the exact signed-main, successful-CI, protected `production-plan` environment and OIDC checks. It skips Terraform checks and the secret-bound planning step: neither `TF_VARS` nor a provider token is needed for discovery. The apply workflow has no discovery operation.

Run **Terraform production plan** from current main, choose **discover-inputs**, and supply that main SHA. The workflow lists Secrets Manager names only in the configured `AWS_REGION`, selecting names containing `mycfc`, `hetzner` or `hcloud` case-insensitively. The summary renders sanitized names, with no values, descriptions, tags, ARNs or raw provider errors. AWS CLI pagination remains enabled. It does not search another account or region, read nested payloads, retrieve any value, or mutate configuration.

The existing plan role must already allow `secretsmanager:ListSecrets` (an account/region inventory permission with resource `*`; it cannot be scoped to individual secret ARNs). Discovery does not grant that permission or broaden the role. Denied access is reported explicitly and stops the run. Review any needed external role change separately. An empty result means only that no top-level names matched; it does not prove the inputs are absent from another secret's payload. A found name is a candidate for a later narrowly scoped review, not authorization to read its value or copy it into GitHub.

## Remaining activation work

Subsequent GitHub-only stages still need an explicit implementation and review for credential provisioning, protected host installation, a current isolated restore drill, policy import and account grants, release-bound technical evidence, the two independent activation signers, readiness checks and worker start. Governance approval and its yearly review are already handled by #109; these are the application's existing execution checks, not additional legal paperwork. Technical evidence currently expires after 90 days, so its renewal must be scheduled separately from the annual policy review.

The legacy full-prefix purge runbook's historical assumption that production rows are disposable test data is not an authorization to erase today's data. Establish actual media state and treatment before selecting purge or backfill. The normal Terraform apply workflow intentionally rejects deletion, including temporary purge-identity teardown; any required teardown needs a separate reviewed GitHub operation. No current workflow bypasses those controls.

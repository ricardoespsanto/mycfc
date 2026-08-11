# Retained AWS and runtime-config Terraform

The ECS/RDS/ALB runtime was retired. This module manages the private repair-photo bucket, immutable ECR repository, Amazon SES transactional-email identity, production runtime configuration in SSM Parameter Store and Secrets Manager, deployment alerts and logs, and separate application-runtime and release-agent IAM users for the Hetzner host. PostgreSQL backup storage and the Hetzner server resource are managed by `../hetzner`.

Use the existing remote backend and run `terraform plan` before applying a retained-resource change. Do not restore retired runtime resources from this module.

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

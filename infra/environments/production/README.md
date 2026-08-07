# Retained AWS and transactional-email Terraform

The ECS/RDS/ALB runtime was retired. This module manages the private repair-photo bucket, immutable ECR repository, and Amazon SES transactional-email identity. PostgreSQL backup storage and the Hetzner host are managed by `../hetzner`.

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

Review the plan, then apply it. The SMTP password and HMAC key are sensitive Terraform outputs stored in the encrypted remote state. Retrieve each with `terraform output -raw <name>` and put them directly into the root-owned `/etc/mycfc/mycfc.env`; do not paste them into tickets, logs, or shell history.

```text
EMAIL_VERIFICATION_HMAC_KEY_B64 = email_verification_hmac_key_b64
SMTP_HOST                       = ses_smtp_host
SMTP_PORT                       = 587
SMTP_USERNAME                   = ses_smtp_username
SMTP_PASSWORD                   = ses_smtp_password
SMTP_FROM_ADDRESS               = ses_from_address
SMTP_FROM_NAME                  = MyCFC
SMTP_TLS_MODE                   = starttls
SMTP_TIMEOUT                    = 10s
```

Easy DKIM verification can take several minutes after DNS publication. Confirm both the identity and account before releasing:

```sh
aws sesv2 get-email-identity --region eu-west-1 --email-identity mycfcoimbra.com \
  --query '{Verified:VerifiedForSendingStatus,Dkim:DkimAttributes.Status}'
aws sesv2 get-account --region eu-west-1 \
  --query '{SendingEnabled:SendingEnabled,ProductionAccessEnabled:ProductionAccessEnabled,EnforcementStatus:EnforcementStatus}'
```

New SES accounts are region-specific and may remain in the sandbox. Terraform cannot approve production access; request it through SES before release if `ProductionAccessEnabled` is false. The [AWS production-access procedure](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html) describes the review.

After installing the Terraform outputs on the host, run `sudo ./verify-ses.sh`. It performs a STARTTLS-authenticated SMTP delivery to the AWS SES success simulator and does not send mail to a real recipient.

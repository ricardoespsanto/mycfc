locals {
  ses_from_address     = "${var.ses_from_local_part}@${var.domain_name}"
  ses_mail_from_domain = "${var.ses_mail_from_subdomain}.${var.domain_name}"
  ses_smtp_endpoint    = "email-smtp.${var.aws_region}.amazonaws.com"
}

data "cloudflare_zones" "application" {
  name      = var.domain_name
  status    = "active"
  max_items = 1
}

locals {
  cloudflare_zone_id = one(data.cloudflare_zones.application.result).id
}

resource "aws_sesv2_email_identity" "application" {
  email_identity = var.domain_name

  dkim_signing_attributes {
    next_signing_key_length = "RSA_2048_BIT"
  }
}

resource "cloudflare_dns_record" "ses_dkim" {
  count = 3

  zone_id = local.cloudflare_zone_id
  name    = "${aws_sesv2_email_identity.application.dkim_signing_attributes[0].tokens[count.index]}._domainkey.${var.domain_name}"
  type    = "CNAME"
  content = "${aws_sesv2_email_identity.application.dkim_signing_attributes[0].tokens[count.index]}.dkim.amazonses.com"
  ttl     = 300
  proxied = false
  comment = "Amazon SES Easy DKIM for MyCFC transactional email"
}

resource "cloudflare_dns_record" "ses_mail_from_mx" {
  zone_id  = local.cloudflare_zone_id
  name     = local.ses_mail_from_domain
  type     = "MX"
  content  = "feedback-smtp.${var.aws_region}.amazonses.com"
  priority = 10
  ttl      = 300
  proxied  = false
  comment  = "Amazon SES custom MAIL FROM bounce handling"
}

resource "cloudflare_dns_record" "ses_mail_from_spf" {
  zone_id = local.cloudflare_zone_id
  name    = local.ses_mail_from_domain
  type    = "TXT"
  content = "v=spf1 include:amazonses.com -all"
  ttl     = 300
  proxied = false
  comment = "Amazon SES custom MAIL FROM SPF policy"
}

resource "aws_sesv2_email_identity_mail_from_attributes" "application" {
  email_identity         = aws_sesv2_email_identity.application.email_identity
  mail_from_domain       = local.ses_mail_from_domain
  behavior_on_mx_failure = "REJECT_MESSAGE"

  depends_on = [
    cloudflare_dns_record.ses_mail_from_mx,
    cloudflare_dns_record.ses_mail_from_spf,
  ]
}

resource "aws_sesv2_account_suppression_attributes" "application" {
  suppressed_reasons = ["BOUNCE", "COMPLAINT"]
}

resource "aws_iam_user" "ses_smtp" {
  name = "${local.name}-ses-smtp"
  tags = local.tags
}

data "aws_iam_policy_document" "ses_smtp" {
  statement {
    sid       = "SendVerificationEmail"
    effect    = "Allow"
    actions   = ["ses:SendRawEmail"]
    resources = [aws_sesv2_email_identity.application.arn]

    condition {
      test     = "StringEquals"
      variable = "ses:FromAddress"
      values   = [local.ses_from_address]
    }
  }
}

resource "aws_iam_user_policy" "ses_smtp" {
  name   = "send-verification-email"
  user   = aws_iam_user.ses_smtp.name
  policy = data.aws_iam_policy_document.ses_smtp.json
}

resource "aws_iam_access_key" "ses_smtp" {
  user = aws_iam_user.ses_smtp.name
}

resource "random_id" "email_verification_hmac_key" {
  byte_length = 32

  keepers = {
    domain = var.domain_name
  }
}

output "ses_identity_arn" {
  value = aws_sesv2_email_identity.application.arn
}

output "ses_identity_verification_status" {
  value = aws_sesv2_email_identity.application.verification_status
}

output "ses_dkim_status" {
  value = aws_sesv2_email_identity.application.dkim_signing_attributes[0].status
}

output "ses_smtp_host" {
  value = local.ses_smtp_endpoint
}

output "ses_smtp_username" {
  value     = aws_iam_access_key.ses_smtp.id
  sensitive = true
}

output "ses_smtp_password" {
  value     = aws_iam_access_key.ses_smtp.ses_smtp_password_v4
  sensitive = true
}

output "ses_from_address" {
  value = local.ses_from_address
}

output "email_verification_hmac_key_b64" {
  value     = random_id.email_verification_hmac_key.b64_std
  sensitive = true
}

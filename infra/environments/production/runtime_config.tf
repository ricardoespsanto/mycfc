locals {
  runtime_parameter_prefix = "/${var.project_name}/${var.environment}"
  runtime_secret_name      = "${local.runtime_parameter_prefix}/app-secrets"
  host_runtime_user_name   = "${local.name}-host-runtime"
  release_agent_user_name  = "${local.name}-release-agent"

  runtime_parameters = {
    "base-url"                 = "https://${var.domain_name}"
    "db/host"                  = var.database_host
    "db/port"                  = tostring(var.database_port)
    "db/name"                  = var.database_name
    "db/user"                  = var.app_db_username
    "db/bootstrap-user"        = var.postgres_username
    "db/migration-user"        = var.migration_db_username
    "db/sslmode"               = var.database_sslmode
    "smtp/host"                = local.ses_smtp_endpoint
    "smtp/port"                = tostring(var.smtp_port)
    "smtp/from-address"        = local.ses_from_address
    "smtp/from-name"           = var.smtp_from_name
    "smtp/tls-mode"            = var.smtp_tls_mode
    "smtp/timeout"             = var.smtp_timeout
    "turnstile/site-key"       = var.turnstile_site_key
    "s3/bucket-name"           = aws_s3_bucket.repairs.bucket
    "s3/force-path-style"      = "false"
    "calendar/competition-id"  = var.calendar_competition_id
    "calendar/training-id"     = var.calendar_training_id
    "calendar/social-id"       = var.calendar_social_id
    "calendar/cleanups-id"     = var.calendar_cleanups_id
    "gallery-url"              = var.gallery_url
    "consent/terms/version"    = var.consent_terms_version
    "consent/terms/sha256"     = var.consent_terms_sha256
    "consent/terms/url"        = var.consent_terms_url
    "consent/image/version"    = var.consent_image_version
    "consent/image/sha256"     = var.consent_image_sha256
    "consent/image/url"        = var.consent_image_url
    "consent/minor/version"    = var.consent_minor_version
    "consent/minor/sha256"     = var.consent_minor_sha256
    "consent/minor/url"        = var.consent_minor_url
    "legal/privacy-url"        = var.privacy_notice_url
    "legal/cookies-url"        = var.cookie_notice_url
    "legal/rights-contact"     = var.data_rights_contact
    "log-level"                = var.log_level
    "trusted-proxy-cidrs"      = join(",", var.trusted_proxy_cidrs)
    "release/repository"       = "${var.github_org}/${var.github_repo}"
    "db/max-conns"             = tostring(var.db_max_conns)
    "db/min-conns"             = tostring(var.db_min_conns)
    "db/max-conn-lifetime"     = var.db_max_conn_lifetime
    "db/max-conn-idle-time"    = var.db_max_conn_idle_time
    "db/health-check-period"   = var.db_health_check_period
    "session/lifetime"         = var.session_lifetime
    "session/idle-timeout"     = var.session_idle_timeout
    "http/max-request-bytes"   = tostring(var.max_request_bytes)
    "http/max-photo-bytes"     = tostring(var.max_photo_bytes)
    "http/read-header-timeout" = var.http_read_header_timeout
    "http/read-timeout"        = var.http_read_timeout
    "http/write-timeout"       = var.http_write_timeout
    "http/idle-timeout"        = var.http_idle_timeout
    "http/shutdown-timeout"    = var.shutdown_timeout
    "release/check-timeout"    = var.release_check_timeout
    "release/check-cache-ttl"  = var.release_check_cache_ttl
  }

  runtime_secret = {
    POSTGRES_PASSWORD               = var.postgres_password
    APP_DB_PASSWORD                 = var.app_db_password
    MIGRATION_DB_PASSWORD           = var.migration_db_password
    CSRF_AUTH_KEY_B64               = random_id.csrf_auth_key.b64_std
    EMAIL_VERIFICATION_HMAC_KEY_B64 = random_id.email_verification_hmac_key.b64_std
    TURNSTILE_SECRET_KEY            = var.turnstile_secret_key
    SMTP_USERNAME                   = aws_iam_access_key.ses_smtp.id
    SMTP_PASSWORD                   = aws_iam_access_key.ses_smtp.ses_smtp_password_v4
    GOOGLE_CALENDAR_API_KEY         = var.google_calendar_api_key
  }
}

resource "random_id" "csrf_auth_key" {
  byte_length = 32

  keepers = {
    environment = var.environment
  }
}

resource "aws_ssm_parameter" "runtime" {
  for_each = local.runtime_parameters

  name        = "${local.runtime_parameter_prefix}/${each.key}"
  description = "MyCFC production runtime setting ${each.key}"
  type        = "String"
  value       = each.value

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_secretsmanager_secret" "runtime" {
  name        = local.runtime_secret_name
  description = "MyCFC production application secrets"

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_secretsmanager_secret_version" "runtime" {
  secret_id     = aws_secretsmanager_secret.runtime.id
  secret_string = jsonencode(local.runtime_secret)
}

resource "aws_iam_user" "host_runtime" {
  name = local.host_runtime_user_name
  tags = local.tags
}

resource "aws_iam_access_key" "host_runtime" {
  user = aws_iam_user.host_runtime.name
}

data "aws_iam_policy_document" "host_runtime" {
  dynamic "statement" {
    for_each = var.release_agent_cutover_complete ? [] : [1]

    content {
      sid    = "PullProductionImagesDuringReleaseAgentCutover"
      effect = "Allow"
      actions = [
        "ecr:BatchCheckLayerAvailability",
        "ecr:BatchGetImage",
        "ecr:DescribeImages",
        "ecr:GetDownloadUrlForLayer",
      ]
      resources = [aws_ecr_repository.app.arn]
    }
  }

  dynamic "statement" {
    for_each = var.release_agent_cutover_complete ? [] : [1]

    content {
      sid       = "AuthenticateToECRDuringReleaseAgentCutover"
      effect    = "Allow"
      actions   = ["ecr:GetAuthorizationToken"]
      resources = ["*"]
    }
  }

  dynamic "statement" {
    for_each = var.release_agent_cutover_complete ? [] : [1]

    content {
      sid    = "WriteDeploymentLogsDuringReleaseAgentCutover"
      effect = "Allow"
      actions = [
        "logs:CreateLogStream",
        "logs:PutLogEvents",
      ]
      resources = ["${aws_cloudwatch_log_group.deployment.arn}:*"]
    }
  }
  statement {
    sid     = "ReadRuntimeParameters"
    effect  = "Allow"
    actions = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = [
      "arn:aws:ssm:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:parameter${local.runtime_parameter_prefix}/*",
    ]
  }

  statement {
    sid       = "ReadRuntimeSecret"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.runtime.arn]
  }

  statement {
    sid       = "UseRepairPhotoBucket"
    effect    = "Allow"
    actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
    resources = ["${aws_s3_bucket.repairs.arn}/*"]
  }
}

resource "aws_iam_user_policy" "host_runtime" {
  name   = "host-runtime"
  user   = aws_iam_user.host_runtime.name
  policy = data.aws_iam_policy_document.host_runtime.json
}

resource "aws_iam_user" "release_agent" {
  name = local.release_agent_user_name
  tags = local.tags
}

resource "aws_iam_access_key" "release_agent" {
  user = aws_iam_user.release_agent.name
}

data "aws_iam_policy_document" "release_agent" {
  statement {
    sid    = "PullProductionImages"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:DescribeImages",
      "ecr:GetDownloadUrlForLayer",
    ]
    resources = [aws_ecr_repository.app.arn]
  }

  statement {
    sid       = "AuthenticateToECR"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "WriteDeploymentLogs"
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.deployment.arn}:*"]
  }
}

resource "aws_iam_user_policy" "release_agent" {
  name   = "release-agent"
  user   = aws_iam_user.release_agent.name
  policy = data.aws_iam_policy_document.release_agent.json
}

output "runtime_parameter_prefix" {
  value = local.runtime_parameter_prefix
}

output "runtime_secret_arn" {
  value = aws_secretsmanager_secret.runtime.arn
}

output "host_runtime_access_key_id" {
  value     = aws_iam_access_key.host_runtime.id
  sensitive = true
}

output "host_runtime_secret_access_key" {
  value     = aws_iam_access_key.host_runtime.secret
  sensitive = true
}

output "release_agent_access_key_id" {
  value     = aws_iam_access_key.release_agent.id
  sensitive = true
}

output "release_agent_secret_access_key" {
  value     = aws_iam_access_key.release_agent.secret
  sensitive = true
}

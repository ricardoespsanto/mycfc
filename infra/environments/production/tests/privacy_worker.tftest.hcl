mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "eu-west-1"
    }
  }
}

mock_provider "cloudflare" {}
mock_provider "random" {}

variables {
  route53_zone_id         = "ZAAAAAAAAAAAAA"
  github_org              = "ricardoespsanto"
  github_repo             = "mycfc"
  calendar_competition_id = "competition@example.com"
  calendar_training_id    = "training@example.com"
  calendar_social_id      = "social@example.com"
  calendar_cleanups_id    = "cleanups@example.com"
  google_calendar_api_key = "test-key"
  gallery_url             = "https://mycfcoimbra.com/gallery"
  consent_terms_version   = "test"
  consent_terms_sha256    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  consent_image_version   = "test"
  consent_image_sha256    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  consent_minor_version   = "test"
  consent_minor_sha256    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
  image_digest            = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
  consent_terms_url       = "https://mycfcoimbra.com/legal/terms"
  consent_image_url       = "https://mycfcoimbra.com/legal/image"
  consent_minor_url       = "https://mycfcoimbra.com/legal/minor"
  privacy_notice_url      = "https://mycfcoimbra.com/legal/privacy"
  cookie_notice_url       = "https://mycfcoimbra.com/legal/cookies"
  data_rights_contact     = "privacy@example.com"
  image_git_sha           = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
  postgres_password       = "test-bootstrap"
  app_db_username         = "mycfc_app"
  app_db_password         = "test-app"
  migration_db_username   = "mycfc_migrator"
  migration_db_password   = "test-migrator"
  turnstile_site_key      = "test-site-key"
  turnstile_secret_key    = "test-secret-key"
}

run "defaults_are_inert" {
  command = plan

  plan_options {
    target = [
      aws_cloudwatch_log_group.privacy_worker,
      aws_iam_policy.privacy_worker_boundary,
      aws_iam_user.privacy_worker,
      aws_iam_user_policy.privacy_worker,
      aws_secretsmanager_secret.privacy_worker,
    ]
  }

  assert {
    condition = (
      length(aws_cloudwatch_log_group.privacy_worker) == 0 &&
      length(aws_iam_policy.privacy_worker_boundary) == 0 &&
      length(aws_iam_user.privacy_worker) == 0 &&
      length(aws_iam_user_policy.privacy_worker) == 0 &&
      length(aws_secretsmanager_secret.privacy_worker) == 0
    )
    error_message = "Default flags must provision no privacy-worker infrastructure."
  }
}

run "s3_grants_require_infrastructure" {
  command = plan

  variables {
    privacy_worker_s3_deletion_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_worker]
  }

  expect_failures = [var.privacy_worker_s3_deletion_enabled]
}

run "rewrite_requires_version_deletion" {
  command = plan

  variables {
    privacy_worker_infrastructure_enabled   = true
    privacy_worker_metadata_rewrite_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_worker]
  }

  expect_failures = [var.privacy_worker_metadata_rewrite_enabled]
}

run "infrastructure_has_no_s3_grant" {
  command = plan

  variables {
    privacy_worker_infrastructure_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_worker]
  }

  assert {
    condition     = !var.privacy_worker_s3_deletion_enabled && !var.privacy_worker_metadata_rewrite_enabled
    error_message = "S3 permissions must remain absent until their separate gate is enabled."
  }

  assert {
    condition     = aws_cloudwatch_log_group.privacy_worker[0].retention_in_days == 90
    error_message = "The dedicated privacy-worker log group must retain logs for exactly 90 days."
  }
}

run "s3_allowlist_is_exact" {
  command = plan

  variables {
    privacy_worker_infrastructure_enabled   = true
    privacy_worker_s3_deletion_enabled      = true
    privacy_worker_metadata_rewrite_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_worker]
  }

  assert {
    condition = toset(local.privacy_worker_media_prefixes) == toset([
      "profiles/*",
      "repairs/*",
      "equipment/*",
    ])
    error_message = "Version deletion must be limited to the three closed application-media prefixes."
  }

  assert {
    condition = toset(local.privacy_worker_rewrite_source_prefixes) == toset([
      "repairs/*",
      "equipment/*",
      ]) && toset(local.privacy_worker_retained_prefixes) == toset([
      "repairs/retained/*",
      "equipment/retained/*",
    ])
    error_message = "Metadata rewrites must read only repair/equipment versions and write only retained subprefixes."
  }

  assert {
    condition = toset(concat(
      local.privacy_worker_version_list_actions,
      local.privacy_worker_version_delete_actions,
      local.privacy_worker_rewrite_read_actions,
      local.privacy_worker_rewrite_write_actions,
      )) == toset([
      "s3:ListBucketVersions",
      "s3:DeleteObjectVersion",
      "s3:GetObjectVersion",
      "s3:PutObject",
    ])
    error_message = "All and only the required version-deletion and metadata-rewrite S3 operations must be present."
  }

  assert {
    condition = alltrue([
      for denied in [
        "s3:*",
        "s3:BypassGovernanceRetention",
        "s3:DeleteObject",
        "s3:GetObject",
        "logs:GetLogEvents",
        "logs:FilterLogEvents",
        ] : !contains(concat(
          local.privacy_worker_secret_actions,
          local.privacy_worker_log_write_actions,
          local.privacy_worker_version_list_actions,
          local.privacy_worker_version_delete_actions,
          local.privacy_worker_rewrite_read_actions,
          local.privacy_worker_rewrite_write_actions,
      ), denied)
    ])
    error_message = "The privacy-worker policy contains a forbidden wildcard, ordinary object operation, log-read, or unrelated-service permission."
  }

}

run "backup_cleanup_failure_alerts_immediately" {
  command = plan

  plan_options {
    target = [
      aws_cloudwatch_log_metric_filter.backup_noncurrent_cleanup_failure,
      aws_cloudwatch_metric_alarm.backup_noncurrent_cleanup_failure,
    ]
  }

  assert {
    condition = (
      !strcontains(aws_cloudwatch_log_metric_filter.backup_noncurrent_cleanup_failure.pattern, "(") &&
      !strcontains(aws_cloudwatch_log_metric_filter.backup_noncurrent_cleanup_failure.pattern, ")") &&
      strcontains(aws_cloudwatch_log_metric_filter.backup_noncurrent_cleanup_failure.pattern, "backup_noncurrent_cleanup_failed") &&
      aws_cloudwatch_metric_alarm.backup_noncurrent_cleanup_failure.evaluation_periods == 1 &&
      aws_cloudwatch_metric_alarm.backup_noncurrent_cleanup_failure.datapoints_to_alarm == 1 &&
      aws_cloudwatch_metric_alarm.backup_noncurrent_cleanup_failure.period == 60
    )
    error_message = "A single exact-version backup cleanup failure must alert in the next one-minute period."
  }
}

run "repair_retention_backstop_and_alarm_are_exact" {
  command = plan

  plan_options {
    target = [
      aws_s3_bucket_lifecycle_configuration.repairs,
      aws_cloudwatch_log_metric_filter.privacy_retention_failure,
      aws_cloudwatch_metric_alarm.privacy_retention_failure,
    ]
  }

  assert {
    condition = (
      aws_s3_bucket_lifecycle_configuration.repairs.rule[0].filter[0].prefix == "repairs/" &&
      aws_s3_bucket_lifecycle_configuration.repairs.rule[0].expiration[0].days == 30 &&
      aws_s3_bucket_lifecycle_configuration.repairs.rule[0].noncurrent_version_expiration[0].noncurrent_days == 1 &&
      aws_s3_bucket_lifecycle_configuration.repairs.rule[0].abort_incomplete_multipart_upload[0].days_after_initiation == 7
    )
    error_message = "Repair photos require the 30-day current and one-day noncurrent lifecycle backstop under the exact repair prefix."
  }

  assert {
    condition = (
      strcontains(aws_cloudwatch_log_metric_filter.privacy_retention_failure.pattern, "privacy_retention_sla_breach") &&
      strcontains(aws_cloudwatch_log_metric_filter.privacy_retention_failure.pattern, "privacy_retention_backlog_breach") &&
      strcontains(aws_cloudwatch_log_metric_filter.privacy_retention_failure.pattern, "privacy_retention_failed") &&
      aws_cloudwatch_metric_alarm.privacy_retention_failure.evaluation_periods == 1 &&
      aws_cloudwatch_metric_alarm.privacy_retention_failure.datapoints_to_alarm == 1 &&
      aws_cloudwatch_metric_alarm.privacy_retention_failure.period == 60
    )
    error_message = "A retention failure or SLA breach must alert in the next one-minute period."
  }
}

run "privacy_restore_drill_failures_alert_immediately" {
  command = plan

  plan_options {
    target = [
      aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure,
      aws_cloudwatch_metric_alarm.privacy_restore_drill_failure,
    ]
  }

  assert {
    condition = (
      !strcontains(aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure.pattern, "(") &&
      !strcontains(aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure.pattern, ")") &&
      strcontains(aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure.pattern, "privacy_restore_drill_failed") &&
      strcontains(aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure.pattern, "privacy_restore_promotion_gate_failed") &&
      aws_cloudwatch_metric_alarm.privacy_restore_drill_failure.evaluation_periods == 1 &&
      aws_cloudwatch_metric_alarm.privacy_restore_drill_failure.datapoints_to_alarm == 1 &&
      aws_cloudwatch_metric_alarm.privacy_restore_drill_failure.treat_missing_data == "notBreaching"
    )
    error_message = "Restore-drill or promotion-attestation failure must alarm on one event without invalid pattern grouping."
  }
}

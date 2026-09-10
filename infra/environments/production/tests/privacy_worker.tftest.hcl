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

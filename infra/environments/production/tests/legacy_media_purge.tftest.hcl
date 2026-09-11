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

  mock_resource "aws_iam_policy" {
    defaults = {
      arn = "arn:aws:iam::123456789012:policy/mycfc-production-legacy-media-purge-boundary"
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

run "identity_is_absent_by_default" {
  command = plan

  plan_options {
    target = [
      aws_iam_policy.legacy_media_purge_boundary,
      aws_iam_user.legacy_media_purge,
      aws_iam_user_policy.legacy_media_purge,
    ]
  }

  assert {
    condition = (
      length(aws_iam_policy.legacy_media_purge_boundary) == 0 &&
      length(aws_iam_user.legacy_media_purge) == 0 &&
      length(aws_iam_user_policy.legacy_media_purge) == 0
    )
    error_message = "The temporary purge identity must be absent by default."
  }
}

run "identity_permissions_are_exact" {
  command = apply

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_deletion_enabled      = true
    legacy_media_purge_write_fence_enabled   = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "12h")
  }

  plan_options {
    target = [
      aws_iam_policy.legacy_media_purge_boundary,
      aws_iam_user.legacy_media_purge,
      aws_iam_user_policy.legacy_media_purge,
    ]
  }

  assert {
    condition = toset(local.legacy_media_purge_prefixes) == toset([
      "profiles/*",
      "repairs/*",
      "equipment/*",
    ])
    error_message = "The purge identity must be limited to the three closed legacy-media prefixes."
  }

  assert {
    condition = (
      aws_iam_user.legacy_media_purge[0].permissions_boundary == aws_iam_policy.legacy_media_purge_boundary[0].arn &&
      jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy) == jsondecode(aws_iam_policy.legacy_media_purge_boundary[0].policy)
    )
    error_message = "The temporary identity must attach the exact same rendered policy as its permissions boundary."
  }

  assert {
    condition = (
      length(jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy).Statement) == 2 &&
      toset(flatten([for statement in jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy).Statement : statement.Action])) ==
      toset(["s3:ListBucketVersions", "s3:DeleteObjectVersion"]) &&
      alltrue([
        for statement in jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy).Statement :
        statement.Condition.DateLessThan["aws:CurrentTime"] == var.legacy_media_purge_permission_expires_at
      ])
    )
    error_message = "The rendered purge policy must contain only the exact expiring inventory and deletion statements."
  }

  assert {
    condition = (
      toset(local.legacy_media_purge_inventory_actions) == toset(["s3:ListBucketVersions"]) &&
      toset(local.legacy_media_purge_deletion_actions) == toset(["s3:DeleteObjectVersion"])
    )
    error_message = "The purge identity must have only version listing and explicit version deletion."
  }

  assert {
    condition = alltrue([
      for denied in [
        "s3:*",
        "s3:BypassGovernanceRetention",
        "s3:DeleteObject",
        "s3:GetObject",
        "s3:PutObject",
        "iam:*",
        "logs:*",
      ] : !contains(concat(local.legacy_media_purge_inventory_actions, local.legacy_media_purge_deletion_actions), denied)
    ])
    error_message = "The purge identity contains a forbidden broad, ordinary-object, IAM, or log permission."
  }

}

run "identity_requires_expiry" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled = true
  }

  plan_options {
    target = [aws_iam_user.legacy_media_purge]
  }

  expect_failures = [var.legacy_media_purge_identity_enabled]
}

run "deletion_requires_identity" {
  command = plan

  variables {
    legacy_media_purge_deletion_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.legacy_media_purge]
  }

  expect_failures = [var.legacy_media_purge_deletion_enabled]
}

run "deletion_requires_write_fence" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_deletion_enabled      = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "12h")
  }

  plan_options {
    target = [aws_iam_user_policy.legacy_media_purge]
  }

  expect_failures = [var.legacy_media_purge_deletion_enabled]
}

run "inventory_phase_has_no_delete_permission" {
  command = apply

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "12h")
  }

  plan_options {
    target = [aws_iam_user_policy.legacy_media_purge]
  }

  assert {
    condition = (
      length(jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy).Statement) == 1 &&
      jsondecode(aws_iam_user_policy.legacy_media_purge[0].policy).Statement[0].Action == ["s3:ListBucketVersions"]
    )
    error_message = "The rendered initial inventory policy must not include deletion permission."
  }
}

run "expiry_must_be_in_the_future" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "-1h")
  }

  plan_options { target = [aws_iam_user.legacy_media_purge] }
  expect_failures = [var.legacy_media_purge_identity_enabled]
}

run "expiry_must_be_no_more_than_twenty_four_hours" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "25h")
  }

  plan_options { target = [aws_iam_user.legacy_media_purge] }
  expect_failures = [var.legacy_media_purge_identity_enabled]
}

run "expiry_must_be_a_real_timestamp" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_permission_expires_at = "2026-99-99T99:99:99Z"
  }

  plan_options { target = [aws_iam_user.legacy_media_purge] }
  expect_failures = [var.legacy_media_purge_identity_enabled]
}

run "expired_deadline_does_not_block_inert_teardown" {
  command = plan

  variables {
    legacy_media_purge_identity_enabled      = false
    legacy_media_purge_deletion_enabled      = false
    legacy_media_purge_write_fence_enabled   = false
    legacy_media_purge_permission_expires_at = "2020-01-01T00:00:00Z"
  }

  plan_options { target = [aws_iam_user.legacy_media_purge] }

  assert {
    condition     = length(aws_iam_user.legacy_media_purge) == 0
    error_message = "An expired deadline must not block removal of every temporary purge resource."
  }
}

run "write_fence_denies_every_legacy_prefix" {
  command = apply

  variables {
    legacy_media_purge_identity_enabled      = true
    legacy_media_purge_write_fence_enabled   = true
    legacy_media_purge_permission_expires_at = timeadd(timestamp(), "12h")
  }

  plan_options { target = [aws_s3_bucket_policy.repairs] }

  assert {
    condition = (
      length([for statement in jsondecode(aws_s3_bucket_policy.repairs.policy).Statement : statement if statement.Sid == "DenyLegacyMediaWritesDuringPurge"]) == 1 &&
      one([for statement in jsondecode(aws_s3_bucket_policy.repairs.policy).Statement : statement if statement.Sid == "DenyLegacyMediaWritesDuringPurge"]).Effect == "Deny" &&
      toset(one([for statement in jsondecode(aws_s3_bucket_policy.repairs.policy).Statement : statement if statement.Sid == "DenyLegacyMediaWritesDuringPurge"]).Action) == toset(["s3:DeleteObject", "s3:PutObject"]) &&
      toset(one([for statement in jsondecode(aws_s3_bucket_policy.repairs.policy).Statement : statement if statement.Sid == "DenyLegacyMediaWritesDuringPurge"]).Resource) == toset([
        "${aws_s3_bucket.repairs.arn}/profiles/*",
        "${aws_s3_bucket.repairs.arn}/repairs/*",
        "${aws_s3_bucket.repairs.arn}/equipment/*",
      ]) &&
      one([for statement in jsondecode(aws_s3_bucket_policy.repairs.policy).Statement : statement if statement.Sid == "DenyLegacyMediaWritesDuringPurge"]).Condition.DateLessThan["aws:CurrentTime"] == var.legacy_media_purge_permission_expires_at
    )
    error_message = "The purge write fence must render as an expiring object-write/delete deny over exactly the three legacy prefixes."
  }
}

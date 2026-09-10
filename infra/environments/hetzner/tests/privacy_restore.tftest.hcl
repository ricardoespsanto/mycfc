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

mock_provider "hcloud" {}

variables {
  ssh_public_key        = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestOperatorKey"
  deploy_ssh_public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey"
}

run "defaults_are_inert" {
  command = plan

  plan_options {
    target = [
      aws_kms_key.privacy_restore_ledger,
      aws_s3_bucket.privacy_restore_ledger,
      aws_iam_user.privacy_restore_writer,
      aws_iam_user.privacy_restore_reader,
      aws_iam_user_policy.privacy_restore_writer,
      aws_iam_user_policy.privacy_restore_reader,
    ]
  }

  assert {
    condition = (
      length(aws_kms_key.privacy_restore_ledger) == 0 &&
      length(aws_s3_bucket.privacy_restore_ledger) == 0 &&
      length(aws_iam_user.privacy_restore_writer) == 0 &&
      length(aws_iam_user.privacy_restore_reader) == 0 &&
      length(aws_iam_user_policy.privacy_restore_writer) == 0 &&
      length(aws_iam_user_policy.privacy_restore_reader) == 0
    )
    error_message = "Default flags must provision no privacy-restore infrastructure or access policy."
  }
}

run "write_access_requires_infrastructure" {
  command = plan

  variables {
    privacy_restore_ledger_write_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_restore_writer]
  }

  expect_failures = [var.privacy_restore_ledger_write_enabled]
}

run "replay_access_requires_infrastructure" {
  command = plan

  variables {
    privacy_restore_ledger_replay_enabled = true
  }

  plan_options {
    target = [aws_iam_user_policy.privacy_restore_reader]
  }

  expect_failures = [var.privacy_restore_ledger_replay_enabled]
}

run "infrastructure_is_protected_but_has_no_access" {
  command = plan

  variables {
    privacy_restore_infrastructure_enabled = true
  }

  plan_options {
    target = [
      aws_s3_bucket_object_lock_configuration.privacy_restore_ledger,
      aws_s3_bucket_lifecycle_configuration.privacy_restore_ledger,
      aws_iam_user_policy.privacy_restore_writer,
      aws_iam_user_policy.privacy_restore_reader,
    ]
  }

  assert {
    condition = (
      aws_s3_bucket.privacy_restore_ledger[0].object_lock_enabled &&
      aws_s3_bucket_versioning.privacy_restore_ledger[0].versioning_configuration[0].status == "Enabled" &&
      aws_s3_bucket_object_lock_configuration.privacy_restore_ledger[0].rule[0].default_retention[0].mode == "COMPLIANCE" &&
      aws_s3_bucket_object_lock_configuration.privacy_restore_ledger[0].rule[0].default_retention[0].years == 2
    )
    error_message = "The restore ledger must use versioning and two-year compliance-mode object lock."
  }

  assert {
    condition = (
      length(aws_iam_user_policy.privacy_restore_writer) == 0 &&
      length(aws_iam_user_policy.privacy_restore_reader) == 0
    )
    error_message = "Provisioning the ledger must not grant write or replay access."
  }
}

run "access_allowlists_are_exact" {
  command = plan

  variables {
    privacy_restore_infrastructure_enabled = true
    privacy_restore_ledger_write_enabled   = true
    privacy_restore_ledger_replay_enabled  = true
  }

  plan_options {
    target = [
      aws_iam_user_policy.privacy_restore_writer,
      aws_iam_user_policy.privacy_restore_reader,
    ]
  }

  assert {
    condition = toset(local.privacy_restore_writer_actions) == toset([
      "s3:GetObjectVersion",
      "s3:PutObject",
      ]) && toset(local.privacy_restore_reader_actions) == toset([
      "s3:GetObjectVersion",
    ])
    error_message = "Ledger access must be limited to append verification and offline version reads."
  }

  assert {
    condition = alltrue([
      for denied in [
        "s3:*",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "s3:GetObject",
        "kms:*",
        "kms:ScheduleKeyDeletion",
        ] : !contains(concat(
          local.privacy_restore_writer_actions,
          local.privacy_restore_reader_actions,
          ["s3:ListBucketVersions", "kms:Encrypt", "kms:GenerateDataKey", "kms:Decrypt"],
      ), denied)
    ])
    error_message = "The privacy restore roles contain a destructive, wildcard, or ordinary object permission."
  }
}

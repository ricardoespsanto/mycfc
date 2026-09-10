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

run "server_backup_intent_is_explicit" {
  command = plan

  plan_options {
    target = [hcloud_server.app]
  }

  assert {
    condition     = hcloud_server.app.backups
    error_message = "The application server must explicitly request Hetzner backups."
  }
}

run "defaults_are_inert" {
  command = plan

  plan_options {
    target = [
      aws_kms_key.privacy_restore_ledger,
      aws_s3_bucket.privacy_restore_ledger,
      aws_iam_user.privacy_restore_writer,
      aws_iam_user.privacy_restore_reader,
      aws_iam_role.privacy_restore_broker,
      aws_lambda_function.privacy_restore_broker,
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
      length(aws_iam_role.privacy_restore_broker) == 0 &&
      length(aws_lambda_function.privacy_restore_broker) == 0 &&
      length(aws_iam_user_policy.privacy_restore_writer) == 0 &&
      length(aws_iam_user_policy.privacy_restore_reader) == 0
    )
    error_message = "Default flags must provision no privacy-restore infrastructure or access policy."
  }
}

run "backup_version_expiration_is_inert" {
  command = plan

  plan_options {
    target = [
      aws_s3_bucket_lifecycle_configuration.postgres_backups,
      aws_iam_user_policy.postgres_backups,
    ]
  }

  assert {
    condition = (
      !var.postgres_backup_noncurrent_cleanup_enabled &&
      length(aws_s3_bucket_lifecycle_configuration.postgres_backups.rule) == 2
    )
    error_message = "A routine plan must retain only the existing current daily/monthly backup rules."
  }
}

run "backup_version_expiration_requires_its_gate" {
  command = plan

  variables {
    postgres_backup_noncurrent_cleanup_enabled = true
  }

  plan_options {
    target = [
      aws_s3_bucket_lifecycle_configuration.postgres_backups,
      aws_iam_user_policy.postgres_backups,
    ]
  }

  assert {
    condition = (
      length(aws_s3_bucket_lifecycle_configuration.postgres_backups.rule) == 6 &&
      alltrue([for rule in aws_s3_bucket_lifecycle_configuration.postgres_backups.rule : rule.noncurrent_version_expiration[0].noncurrent_days == 1 if startswith(rule.id, "expire-noncurrent-")]) &&
      alltrue([for rule in aws_s3_bucket_lifecycle_configuration.postgres_backups.rule : rule.expiration[0].expired_object_delete_marker if startswith(rule.id, "remove-expired-")]) &&
      toset([for rule in aws_s3_bucket_lifecycle_configuration.postgres_backups.rule : rule.filter[0].prefix if startswith(rule.id, "expire-noncurrent-")]) == toset(["daily/", "monthly/"]) &&
      toset([for rule in aws_s3_bucket_lifecycle_configuration.postgres_backups.rule : rule.filter[0].prefix if startswith(rule.id, "remove-expired-")]) == toset(["daily/", "monthly/"])
    )
    error_message = "The destructive backup rules must appear only behind their explicit gate."
  }

  assert {
    condition = (
      toset(local.backup_cleanup_list_actions) == toset(["s3:ListBucketVersions"]) &&
      toset(local.backup_cleanup_actions) == toset(["s3:DeleteObjectVersion"])
    )
    error_message = "Backup cleanup must list versions and delete exact versions only."
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
      one([for rule in aws_s3_bucket_lifecycle_configuration.privacy_restore_ledger[0].rule : rule if rule.id == "expire-ledger-after-evidence-window"]).expiration[0].days == 731 &&
      one([for rule in aws_s3_bucket_lifecycle_configuration.privacy_restore_ledger[0].rule : rule if rule.id == "expire-ledger-after-evidence-window"]).noncurrent_version_expiration[0].noncurrent_days == 1 &&
      one([for rule in aws_s3_bucket_lifecycle_configuration.privacy_restore_ledger[0].rule : rule if rule.id == "remove-expired-ledger-delete-markers"]).expiration[0].expired_object_delete_marker
    )
    error_message = "Ledger payloads must not receive a second two-year noncurrent retention period."
  }

  assert {
    condition = (
      length(aws_iam_user_policy.privacy_restore_writer) == 0 &&
      length(aws_iam_user_policy.privacy_restore_reader) == 0
    )
    error_message = "Provisioning the ledger must not grant write or replay access."
  }


  assert {
    condition = toset(local.privacy_restore_broker_kms_actions) == toset([
      "kms:Decrypt",
      "kms:Encrypt",
      "kms:GenerateDataKey",
      ]) && toset(local.privacy_restore_reader_kms_actions) == toset([
      "kms:Decrypt",
    ])
    error_message = "Only the broker may use ledger cryptography and the replay identity must be decrypt-only."
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
      "lambda:InvokeFunction",
      ]) && toset(local.privacy_restore_broker_object_actions) == toset([
      "s3:GetObject",
      "s3:GetObjectVersion",
      "s3:PutObject",
      ]) && toset(local.privacy_restore_broker_retention_actions) == toset([
      "s3:GetObjectRetention",
      "s3:PutObjectRetention",
      ]) && toset(local.privacy_restore_reader_actions) == toset([
      "s3:GetObjectVersion",
    ])
    error_message = "The worker must only invoke the one-shot broker; direct ledger access belongs only to the broker and offline reader."
  }

  assert {
    condition = (
      aws_lambda_function.privacy_restore_broker[0].reserved_concurrent_executions == 1 &&
      aws_lambda_function.privacy_restore_broker[0].timeout == 15 &&
      aws_lambda_function.privacy_restore_broker[0].memory_size == 128
    )
    error_message = "The ledger broker must retain its bounded execution and concurrency limits."
  }

  assert {
    condition = alltrue([
      for denied in [
        "s3:*",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "kms:*",
        "kms:ScheduleKeyDeletion",
        ] : !contains(concat(
          local.privacy_restore_writer_actions,
          local.privacy_restore_broker_object_actions,
          local.privacy_restore_broker_retention_actions,
          local.privacy_restore_reader_actions,
          ["s3:ListBucketVersions"],
          local.privacy_restore_broker_kms_actions,
          local.privacy_restore_reader_kms_actions,
      ), denied)
    ])
    error_message = "The privacy restore roles contain a destructive, wildcard, key-administration, or unrelated permission."
  }
}

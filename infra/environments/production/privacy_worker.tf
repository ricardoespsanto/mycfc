locals {
  privacy_worker_user_name   = "${local.name}-privacy-worker"
  privacy_worker_secret_name = "/${var.project_name}/${var.environment}/privacy-worker"
  privacy_worker_log_name    = "/${var.project_name}/${var.environment}/privacy-worker"

  privacy_worker_media_prefixes          = ["profiles/*", "repairs/*", "equipment/*"]
  privacy_worker_rewrite_source_prefixes = ["repairs/*", "equipment/*"]
  privacy_worker_retained_prefixes       = ["repairs/retained/*", "equipment/retained/*"]

  privacy_worker_log_write_actions = [
    "logs:CreateLogStream",
    "logs:PutLogEvents",
  ]
  privacy_worker_version_list_actions = [
    "s3:ListBucketVersions",
  ]
  privacy_worker_version_delete_actions = [
    "s3:DeleteObjectVersion",
  ]
  privacy_worker_rewrite_read_actions = [
    "s3:GetObjectVersion",
  ]
  privacy_worker_rewrite_write_actions = [
    "s3:PutObject",
  ]
  privacy_worker_ledger_broker_actions = [
    "lambda:InvokeFunction",
  ]
}

resource "aws_secretsmanager_secret" "privacy_worker" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  name                    = local.privacy_worker_secret_name
  description             = "Empty credential container for the disabled MyCFC privacy worker"
  recovery_window_in_days = 30

  lifecycle {
    prevent_destroy = true
  }
}

# Deliberately no aws_secretsmanager_secret_version is declared. Provisioning this
# container never writes a credential or secret value into Terraform or its state.

resource "aws_cloudwatch_log_group" "privacy_worker" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  name              = local.privacy_worker_log_name
  retention_in_days = 90

  lifecycle {
    prevent_destroy = true
  }
}

data "aws_iam_policy_document" "privacy_worker_boundary" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  statement {
    sid       = "WriteOnlyWorkerLogs"
    effect    = "Allow"
    actions   = local.privacy_worker_log_write_actions
    resources = ["${aws_cloudwatch_log_group.privacy_worker[0].arn}:log-stream:*"]
  }

  dynamic "statement" {
    for_each = var.privacy_worker_s3_deletion_enabled ? [1] : []

    content {
      sid       = "ListOnlyMediaVersions"
      effect    = "Allow"
      actions   = local.privacy_worker_version_list_actions
      resources = [aws_s3_bucket.repairs.arn]

      condition {
        test     = "StringLike"
        variable = "s3:prefix"
        values   = local.privacy_worker_media_prefixes
      }
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_s3_deletion_enabled ? [1] : []

    content {
      sid       = "DeleteOnlyMediaVersions"
      effect    = "Allow"
      actions   = local.privacy_worker_version_delete_actions
      resources = [for prefix in local.privacy_worker_media_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_metadata_rewrite_enabled ? [1] : []

    content {
      sid       = "ReadOnlyRewriteSources"
      effect    = "Allow"
      actions   = local.privacy_worker_rewrite_read_actions
      resources = [for prefix in local.privacy_worker_rewrite_source_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_metadata_rewrite_enabled ? [1] : []

    content {
      sid       = "WriteOnlyRetainedCopies"
      effect    = "Allow"
      actions   = local.privacy_worker_rewrite_write_actions
      resources = [for prefix in local.privacy_worker_retained_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_ledger_broker_invoke_enabled ? [1] : []

    content {
      sid       = "InvokeOnlyRestoreLedgerBroker"
      effect    = "Allow"
      actions   = local.privacy_worker_ledger_broker_actions
      resources = [var.privacy_worker_ledger_broker_function_arn]
    }
  }
}

resource "aws_iam_policy" "privacy_worker_boundary" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  name        = "${local.privacy_worker_user_name}-boundary"
  description = "Maximum permissions boundary for the disabled MyCFC privacy worker"
  policy      = data.aws_iam_policy_document.privacy_worker_boundary[0].json
  tags        = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_iam_user" "privacy_worker" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  name                 = local.privacy_worker_user_name
  permissions_boundary = aws_iam_policy.privacy_worker_boundary[0].arn
  tags                 = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

data "aws_iam_policy_document" "privacy_worker" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  statement {
    sid       = "WriteWorkerLogs"
    effect    = "Allow"
    actions   = local.privacy_worker_log_write_actions
    resources = ["${aws_cloudwatch_log_group.privacy_worker[0].arn}:log-stream:*"]
  }

  dynamic "statement" {
    for_each = var.privacy_worker_s3_deletion_enabled ? [1] : []

    content {
      sid       = "ListMediaVersions"
      effect    = "Allow"
      actions   = local.privacy_worker_version_list_actions
      resources = [aws_s3_bucket.repairs.arn]

      condition {
        test     = "StringLike"
        variable = "s3:prefix"
        values   = local.privacy_worker_media_prefixes
      }
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_s3_deletion_enabled ? [1] : []

    content {
      sid       = "DeleteMediaVersions"
      effect    = "Allow"
      actions   = local.privacy_worker_version_delete_actions
      resources = [for prefix in local.privacy_worker_media_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_metadata_rewrite_enabled ? [1] : []

    content {
      sid       = "ReadRewriteSources"
      effect    = "Allow"
      actions   = local.privacy_worker_rewrite_read_actions
      resources = [for prefix in local.privacy_worker_rewrite_source_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_metadata_rewrite_enabled ? [1] : []

    content {
      sid       = "WriteRetainedCopies"
      effect    = "Allow"
      actions   = local.privacy_worker_rewrite_write_actions
      resources = [for prefix in local.privacy_worker_retained_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_worker_ledger_broker_invoke_enabled ? [1] : []

    content {
      sid       = "InvokeRestoreLedgerBroker"
      effect    = "Allow"
      actions   = local.privacy_worker_ledger_broker_actions
      resources = [var.privacy_worker_ledger_broker_function_arn]
    }
  }
}

resource "aws_iam_user_policy" "privacy_worker" {
  count = var.privacy_worker_infrastructure_enabled ? 1 : 0

  name   = "privacy-worker"
  user   = aws_iam_user.privacy_worker[0].name
  policy = data.aws_iam_policy_document.privacy_worker[0].json

  lifecycle {
    precondition {
      condition = !var.privacy_worker_ledger_broker_invoke_enabled || startswith(
        var.privacy_worker_ledger_broker_function_arn,
        "arn:aws:lambda:${var.aws_region}:${data.aws_caller_identity.current.account_id}:function:",
      )
      error_message = "The privacy worker broker must belong to the configured production AWS account and region."
    }
  }
}

output "privacy_worker_user_name" {
  description = "IAM user name created only when the disabled privacy-worker infrastructure is explicitly provisioned. No access key is created."
  value       = try(aws_iam_user.privacy_worker[0].name, null)
}

output "privacy_worker_secret_arn" {
  description = "ARN of the empty privacy-worker secret container, when provisioned."
  value       = try(aws_secretsmanager_secret.privacy_worker[0].arn, null)
}

output "privacy_worker_log_group_name" {
  description = "Dedicated 90-day privacy-worker log group, when provisioned."
  value       = try(aws_cloudwatch_log_group.privacy_worker[0].name, null)
}

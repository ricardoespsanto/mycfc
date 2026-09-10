locals {
  privacy_restore_ledger_bucket  = "${local.name}-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.region}-privacy-ledger"
  privacy_restore_prefix         = "tombstones/"
  privacy_restore_closure_prefix = "tombstones/closure/"
  privacy_restore_writer_name    = "${local.name}-privacy-restore-writer"
  privacy_restore_reader_name    = "${local.name}-privacy-restore-reader"
  privacy_restore_writer_arn     = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:user/${local.privacy_restore_writer_name}"
  privacy_restore_reader_arn     = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:user/${local.privacy_restore_reader_name}"

  privacy_restore_writer_actions = [
    "s3:GetObject",
    "s3:GetObjectVersion",
    "s3:PutObject",
  ]
  privacy_restore_writer_retention_actions = [
    "s3:GetObjectRetention",
    "s3:PutObjectRetention",
  ]
  privacy_restore_reader_actions = [
    "s3:GetObjectVersion",
  ]
  privacy_restore_writer_kms_actions = [
    "kms:Decrypt",
    "kms:Encrypt",
    "kms:GenerateDataKey",
  ]
  privacy_restore_reader_kms_actions = [
    "kms:Decrypt",
  ]
  privacy_restore_key_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AccountKeyAdministration"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action = [
          "kms:CancelKeyDeletion",
          "kms:CreateAlias",
          "kms:CreateGrant",
          "kms:DeleteAlias",
          "kms:DescribeKey",
          "kms:DisableKey",
          "kms:DisableKeyRotation",
          "kms:EnableKey",
          "kms:EnableKeyRotation",
          "kms:GetKeyPolicy",
          "kms:GetKeyRotationStatus",
          "kms:ListGrants",
          "kms:ListResourceTags",
          "kms:ListRetirableGrants",
          "kms:PutKeyPolicy",
          "kms:RevokeGrant",
          "kms:ScheduleKeyDeletion",
          "kms:TagResource",
          "kms:UntagResource",
          "kms:UpdateAlias",
          "kms:UpdateKeyDescription",
        ]
        Resource = "*"
      },
      {
        Sid    = "LedgerWriterCryptographyOnly"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action   = local.privacy_restore_writer_kms_actions
        Resource = "*"
        Condition = {
          ArnEquals = { "aws:PrincipalArn" = local.privacy_restore_writer_arn }
          StringEquals = {
            "kms:EncryptionContext:aws:s3:arn" = "arn:aws:s3:::${local.privacy_restore_ledger_bucket}"
          }
        }
      },
      {
        Sid    = "LedgerReaderDecryptOnly"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action   = local.privacy_restore_reader_kms_actions
        Resource = "*"
        Condition = {
          ArnEquals = { "aws:PrincipalArn" = local.privacy_restore_reader_arn }
          StringEquals = {
            "kms:EncryptionContext:aws:s3:arn" = "arn:aws:s3:::${local.privacy_restore_ledger_bucket}"
          }
        }
      },
    ]
  })
}

resource "aws_kms_key" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  description             = "SSE-KMS boundary for the independently encrypted ${local.name} privacy restore ledger"
  enable_key_rotation     = true
  deletion_window_in_days = 30
  policy                  = local.privacy_restore_key_policy

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_kms_alias" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  name          = "alias/${local.name}-privacy-restore-ledger"
  target_key_id = aws_kms_key.privacy_restore_ledger[0].key_id
}

resource "aws_s3_bucket" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket              = local.privacy_restore_ledger_bucket
  object_lock_enabled = true

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket                  = aws_s3_bucket.privacy_restore_ledger[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id
  rule { object_ownership = "BucketOwnerEnforced" }
}

resource "aws_s3_bucket_versioning" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id
  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.privacy_restore_ledger[0].arn
      sse_algorithm     = "aws:kms"
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_object_lock_configuration" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id

  depends_on = [aws_s3_bucket_versioning.privacy_restore_ledger]

  rule {
    default_retention {
      mode  = "COMPLIANCE"
      years = 2
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id

  depends_on = [aws_s3_bucket_versioning.privacy_restore_ledger]

  rule {
    id     = "expire-ledger-after-evidence-window"
    status = "Enabled"
    filter { prefix = local.privacy_restore_closure_prefix }
    expiration { days = 731 }
    noncurrent_version_expiration { noncurrent_days = 1 }
  }

  rule {
    id     = "remove-expired-ledger-delete-markers"
    status = "Enabled"
    filter { prefix = local.privacy_restore_closure_prefix }
    expiration { expired_object_delete_marker = true }
  }
}

data "aws_iam_policy_document" "privacy_restore_ledger_bucket" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.privacy_restore_ledger[0].arn, "${aws_s3_bucket.privacy_restore_ledger[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }

  statement {
    sid       = "DenyClosureWithoutComplianceMode"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:object-lock-mode"
      values   = ["COMPLIANCE"]
    }
  }

  statement {
    sid       = "DenyClosureWithoutRetainUntilDate"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Null"
      variable = "s3:object-lock-retain-until-date"
      values   = ["true"]
    }
  }

  statement {
    sid       = "DenyClosureRetentionBeyondEvidenceWindow"
    effect    = "Deny"
    actions   = ["s3:PutObject", "s3:PutObjectRetention"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "NumericGreaterThan"
      variable = "s3:object-lock-remaining-retention-days"
      values   = ["731"]
    }
  }

  statement {
    sid       = "DenyClosureRetentionBelowEvidenceWindow"
    effect    = "Deny"
    actions   = ["s3:PutObject", "s3:PutObjectRetention"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "NumericLessThan"
      variable = "s3:object-lock-remaining-retention-days"
      values   = ["729"]
    }
  }

  statement {
    sid       = "DenyMissingKMSHeader"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Null"
      variable = "s3:x-amz-server-side-encryption"
      values   = ["true"]
    }
  }

  statement {
    sid       = "DenyIncorrectKMSEncryption"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption"
      values   = ["aws:kms"]
    }
  }

  statement {
    sid       = "DenyWrongKMSKey"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption-aws-kms-key-id"
      values   = [aws_kms_key.privacy_restore_ledger[0].arn]
    }
  }
}

resource "aws_s3_bucket_policy" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id
  policy = data.aws_iam_policy_document.privacy_restore_ledger_bucket[0].json
}

resource "aws_iam_user" "privacy_restore_writer" {
  count                = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name                 = local.privacy_restore_writer_name
  permissions_boundary = aws_iam_policy.privacy_restore_writer_boundary[0].arn
}

resource "aws_iam_user" "privacy_restore_reader" {
  count                = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name                 = local.privacy_restore_reader_name
  permissions_boundary = aws_iam_policy.privacy_restore_reader_boundary[0].arn
}

data "aws_iam_policy_document" "privacy_restore_writer_boundary" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_writer_actions
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
  }


  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_writer_retention_actions
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
  }

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_writer_kms_actions
    resources = [aws_kms_key.privacy_restore_ledger[0].arn]
  }
}

resource "aws_iam_policy" "privacy_restore_writer_boundary" {
  count       = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name        = "${local.privacy_restore_writer_name}-boundary"
  description = "Maximum append-and-verify permissions for the privacy restore ledger writer"
  policy      = data.aws_iam_policy_document.privacy_restore_writer_boundary[0].json

  lifecycle { prevent_destroy = true }
}

data "aws_iam_policy_document" "privacy_restore_reader_boundary" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  statement {
    effect    = "Allow"
    actions   = ["s3:ListBucketVersions"]
    resources = [aws_s3_bucket.privacy_restore_ledger[0].arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["${local.privacy_restore_prefix}*"]
    }
  }

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_reader_actions
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
  }

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_reader_kms_actions
    resources = [aws_kms_key.privacy_restore_ledger[0].arn]
  }
}

resource "aws_iam_policy" "privacy_restore_reader_boundary" {
  count       = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name        = "${local.privacy_restore_reader_name}-boundary"
  description = "Maximum offline replay permissions for the privacy restore ledger reader"
  policy      = data.aws_iam_policy_document.privacy_restore_reader_boundary[0].json

  lifecycle { prevent_destroy = true }
}

data "aws_iam_policy_document" "privacy_restore_writer" {
  count = var.privacy_restore_ledger_write_enabled ? 1 : 0

  dynamic "statement" {
    for_each = var.privacy_restore_ledger_write_enabled ? [1] : []
    content {
      sid       = "AppendAndVerifyEncryptedTombstones"
      effect    = "Allow"
      actions   = local.privacy_restore_writer_actions
      resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
    }
  }
  dynamic "statement" {
    for_each = var.privacy_restore_ledger_write_enabled ? [1] : []
    content {
      sid       = "VerifyClosureRetention"
      effect    = "Allow"
      actions   = local.privacy_restore_writer_retention_actions
      resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
    }
  }
  dynamic "statement" {
    for_each = var.privacy_restore_ledger_write_enabled ? [1] : []
    content {
      sid       = "EncryptLedgerObjects"
      effect    = "Allow"
      actions   = local.privacy_restore_writer_kms_actions
      resources = [aws_kms_key.privacy_restore_ledger[0].arn]
    }
  }
}

data "aws_iam_policy_document" "privacy_restore_reader" {
  count = var.privacy_restore_ledger_replay_enabled ? 1 : 0

  statement {
    sid       = "ListEncryptedTombstonesOffline"
    effect    = "Allow"
    actions   = ["s3:ListBucketVersions"]
    resources = [aws_s3_bucket.privacy_restore_ledger[0].arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["${local.privacy_restore_prefix}*"]
    }
  }

  dynamic "statement" {
    for_each = var.privacy_restore_ledger_replay_enabled ? [1] : []
    content {
      sid       = "ReadEncryptedTombstonesOffline"
      effect    = "Allow"
      actions   = local.privacy_restore_reader_actions
      resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}*"]
    }
  }
  dynamic "statement" {
    for_each = var.privacy_restore_ledger_replay_enabled ? [1] : []
    content {
      sid       = "DecryptLedgerObjectsOffline"
      effect    = "Allow"
      actions   = local.privacy_restore_reader_kms_actions
      resources = [aws_kms_key.privacy_restore_ledger[0].arn]
    }
  }
}

resource "aws_iam_user_policy" "privacy_restore_writer" {
  count  = var.privacy_restore_ledger_write_enabled ? 1 : 0
  name   = "privacy-restore-writer"
  user   = aws_iam_user.privacy_restore_writer[0].name
  policy = data.aws_iam_policy_document.privacy_restore_writer[0].json
}

resource "aws_iam_user_policy" "privacy_restore_reader" {
  count  = var.privacy_restore_ledger_replay_enabled ? 1 : 0
  name   = "privacy-restore-reader"
  user   = aws_iam_user.privacy_restore_reader[0].name
  policy = data.aws_iam_policy_document.privacy_restore_reader[0].json
}

output "privacy_restore_ledger_bucket" {
  description = "Restore-independent tombstone ledger bucket, provisioned without credentials or active access."
  value       = try(aws_s3_bucket.privacy_restore_ledger[0].bucket, null)
}

output "privacy_restore_ledger_kms_key_arn" {
  description = "Exact KMS key ARN that ledger writers must supply with every encrypted upload."
  value       = try(aws_kms_key.privacy_restore_ledger[0].arn, null)
}

locals {
  privacy_restore_ledger_bucket = "${local.name}-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.region}-privacy-ledger"
  privacy_restore_prefix        = "tombstones/"

  privacy_restore_writer_actions = [
    "s3:GetObjectVersion",
    "s3:PutObject",
  ]
  privacy_restore_reader_actions = [
    "s3:GetObjectVersion",
  ]
}

resource "aws_kms_key" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  description             = "SSE-KMS boundary for the independently encrypted ${local.name} privacy restore ledger"
  enable_key_rotation     = true
  deletion_window_in_days = 30

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
    filter { prefix = local.privacy_restore_prefix }
    expiration { days = 731 }
    noncurrent_version_expiration { noncurrent_days = 731 }
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
}

resource "aws_s3_bucket_policy" "privacy_restore_ledger" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_restore_ledger[0].id
  policy = data.aws_iam_policy_document.privacy_restore_ledger_bucket[0].json
}

resource "aws_iam_user" "privacy_restore_writer" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name  = "${local.name}-privacy-restore-writer"
}

resource "aws_iam_user" "privacy_restore_reader" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0
  name  = "${local.name}-privacy-restore-reader"
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
      sid       = "EncryptLedgerObjects"
      effect    = "Allow"
      actions   = ["kms:Encrypt", "kms:GenerateDataKey"]
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
      actions   = ["kms:Decrypt"]
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

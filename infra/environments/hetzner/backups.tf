data "aws_caller_identity" "current" {}

data "aws_region" "current" {}

locals {
  backup_bucket               = "${local.name}-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.region}-postgres-backups"
  backup_recovery_prefixes    = ["daily/*", "monthly/*"]
  backup_attestation_prefixes = ["restore-attestations/*", "restore-evidence/*"]
  backup_prefixes             = concat(local.backup_recovery_prefixes, local.backup_attestation_prefixes)
  backup_base_list_actions    = ["s3:ListBucket"]
  backup_cleanup_role_name    = "${local.name}-postgres-backup-cleanup"
  backup_cleanup_list_actions = ["s3:ListBucketVersions"]
  backup_object_actions       = ["s3:GetObject", "s3:PutObject"]
  backup_version_read_actions = ["s3:GetObjectVersion"]
  backup_cleanup_actions      = ["s3:DeleteObjectVersion"]
  backup_encryption_actions   = ["kms:Decrypt", "kms:GenerateDataKey"]
}

resource "aws_kms_key" "postgres_backups" {
  description             = "Envelope encryption for ${local.name} PostgreSQL backups"
  enable_key_rotation     = true
  deletion_window_in_days = 30
}

resource "aws_kms_alias" "postgres_backups" {
  name          = "alias/${local.name}-postgres-backups"
  target_key_id = aws_kms_key.postgres_backups.key_id
}

resource "aws_s3_bucket" "postgres_backups" {
  bucket = local.backup_bucket

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "postgres_backups" {
  bucket                  = aws_s3_bucket.postgres_backups.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }

}

resource "aws_s3_bucket_versioning" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.postgres_backups.arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    id     = "retain-daily-recovery-points"
    status = "Enabled"

    filter { prefix = "daily/" }

    expiration { days = 30 }
  }

  rule {
    id     = "retain-monthly-recovery-points"
    status = "Enabled"

    filter { prefix = "monthly/" }

    expiration { days = 365 }
  }

  rule {
    id     = "retain-privacy-restore-attestations"
    status = "Enabled"

    filter { prefix = "restore-attestations/" }

    expiration { days = 400 }
    noncurrent_version_expiration { noncurrent_days = 1 }
  }

  rule {
    id     = "retain-privacy-restore-evidence"
    status = "Enabled"

    filter { prefix = "restore-evidence/" }

    expiration { days = 400 }
    noncurrent_version_expiration { noncurrent_days = 1 }
  }

  dynamic "rule" {
    for_each = var.postgres_backup_noncurrent_cleanup_enabled ? toset(["daily/", "monthly/"]) : toset([])

    content {
      id     = "expire-noncurrent-${trimsuffix(rule.value, "/")}-backup-versions"
      status = "Enabled"

      filter { prefix = rule.value }

      noncurrent_version_expiration { noncurrent_days = 1 }
    }
  }

  dynamic "rule" {
    for_each = var.postgres_backup_noncurrent_cleanup_enabled ? toset(["daily/", "monthly/"]) : toset([])

    content {
      id     = "remove-expired-${trimsuffix(rule.value, "/")}-backup-delete-markers"
      status = "Enabled"

      filter { prefix = rule.value }

      expiration { expired_object_delete_marker = true }
    }
  }
}

data "aws_iam_policy_document" "postgres_backups_bucket" {
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.postgres_backups.arn, "${aws_s3_bucket.postgres_backups.arn}/*"]

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

resource "aws_s3_bucket_policy" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id
  policy = data.aws_iam_policy_document.postgres_backups_bucket.json
}

resource "aws_iam_user" "postgres_backups" {
  name                 = "${local.name}-postgres-backups"
  permissions_boundary = aws_iam_policy.postgres_backups_boundary.arn
}

data "aws_iam_policy_document" "postgres_backups_boundary" {
  statement {
    effect    = "Allow"
    actions   = local.backup_base_list_actions
    resources = [aws_s3_bucket.postgres_backups.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = local.backup_prefixes
    }
  }

  statement {
    effect    = "Allow"
    actions   = local.backup_object_actions
    resources = [for prefix in local.backup_prefixes : "${aws_s3_bucket.postgres_backups.arn}/${prefix}"]
  }

  statement {
    effect    = "Allow"
    actions   = local.backup_version_read_actions
    resources = [for prefix in local.backup_recovery_prefixes : "${aws_s3_bucket.postgres_backups.arn}/${prefix}"]
  }

  statement {
    effect    = "Allow"
    actions   = local.backup_cleanup_list_actions
    resources = [aws_s3_bucket.postgres_backups.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = local.backup_recovery_prefixes
    }
  }

  statement {
    effect    = "Allow"
    actions   = local.backup_encryption_actions
    resources = [aws_kms_key.postgres_backups.arn]
  }
}

resource "aws_iam_policy" "postgres_backups_boundary" {
  name        = "${local.name}-postgres-backups-boundary"
  description = "Maximum write-only backup and restore-read permissions without version deletion"
  policy      = data.aws_iam_policy_document.postgres_backups_boundary.json

  lifecycle { prevent_destroy = true }
}

data "aws_iam_policy_document" "postgres_backups" {
  statement {
    sid       = "ListBackupObjects"
    effect    = "Allow"
    actions   = concat(local.backup_base_list_actions, local.backup_cleanup_list_actions)
    resources = [aws_s3_bucket.postgres_backups.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = local.backup_recovery_prefixes
    }
  }

  statement {
    sid       = "ReadAndWriteBackupObjects"
    effect    = "Allow"
    actions   = local.backup_object_actions
    resources = [for prefix in local.backup_prefixes : "${aws_s3_bucket.postgres_backups.arn}/${prefix}"]
  }

  statement {
    sid       = "ReadExactRecoveryPointVersions"
    effect    = "Allow"
    actions   = local.backup_version_read_actions
    resources = [for prefix in local.backup_recovery_prefixes : "${aws_s3_bucket.postgres_backups.arn}/${prefix}"]
  }

  statement {
    sid       = "EnvelopeEncryption"
    effect    = "Allow"
    actions   = local.backup_encryption_actions
    resources = [aws_kms_key.postgres_backups.arn]
  }
}

resource "aws_iam_user_policy" "postgres_backups" {
  name   = "postgres-backups"
  user   = aws_iam_user.postgres_backups.name
  policy = data.aws_iam_policy_document.postgres_backups.json
}

data "aws_iam_policy_document" "postgres_backup_cleanup_boundary" {
  count = var.postgres_backup_cleanup_identity_enabled ? 1 : 0

  statement {
    sid       = "ListRecoveryPointVersions"
    effect    = "Allow"
    actions   = local.backup_cleanup_list_actions
    resources = [aws_s3_bucket.postgres_backups.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = local.backup_recovery_prefixes
    }
  }

  dynamic "statement" {
    for_each = var.postgres_backup_noncurrent_cleanup_enabled ? [1] : []

    content {
      sid       = "DeleteExpiredRecoveryPointVersions"
      effect    = "Allow"
      actions   = local.backup_cleanup_actions
      resources = [for prefix in local.backup_recovery_prefixes : "${aws_s3_bucket.postgres_backups.arn}/${prefix}"]
    }
  }
}

resource "aws_iam_policy" "postgres_backup_cleanup_boundary" {
  count = var.postgres_backup_cleanup_identity_enabled ? 1 : 0

  name        = "${local.backup_cleanup_role_name}-boundary"
  description = "Maximum independently gated PostgreSQL backup version-cleanup permissions"
  policy      = data.aws_iam_policy_document.postgres_backup_cleanup_boundary[0].json

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role" "postgres_backup_cleanup" {
  count = var.postgres_backup_cleanup_identity_enabled ? 1 : 0

  name = local.backup_cleanup_role_name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowApprovedCredentialRenewers"
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { AWS = sort(tolist(var.postgres_backup_cleanup_assumer_arns)) }
    }]
  })
  max_session_duration = 3600
  permissions_boundary = aws_iam_policy.postgres_backup_cleanup_boundary[0].arn

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role_policy" "postgres_backup_cleanup" {
  count = var.postgres_backup_cleanup_identity_enabled ? 1 : 0

  name   = "postgres-backup-cleanup"
  role   = aws_iam_role.postgres_backup_cleanup[0].name
  policy = data.aws_iam_policy_document.postgres_backup_cleanup_boundary[0].json
}

output "postgres_backup_bucket" {
  value = aws_s3_bucket.postgres_backups.bucket
}

output "postgres_backup_kms_key_arn" {
  value = aws_kms_key.postgres_backups.arn
}

output "postgres_backup_cleanup_role_arn" {
  description = "Dedicated cleanup role issuing sessions of at most one hour when its inert infrastructure gate is enabled."
  value       = try(aws_iam_role.postgres_backup_cleanup[0].arn, null)
}

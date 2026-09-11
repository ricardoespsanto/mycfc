locals {
  legacy_media_purge_user_name         = "${local.name}-legacy-media-purge"
  legacy_media_purge_prefixes          = ["profiles/*", "repairs/*", "equipment/*"]
  legacy_media_purge_inventory_actions = ["s3:ListBucketVersions"]
  legacy_media_purge_deletion_actions  = ["s3:DeleteObjectVersion"]
  legacy_media_purge_policy = {
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "ListOnlyLegacyMediaVersions"
        Effect   = "Allow"
        Action   = local.legacy_media_purge_inventory_actions
        Resource = [aws_s3_bucket.repairs.arn]
        Condition = {
          StringLike   = { "s3:prefix" = local.legacy_media_purge_prefixes }
          DateLessThan = { "aws:CurrentTime" = var.legacy_media_purge_permission_expires_at }
        }
      }
      ], var.legacy_media_purge_deletion_enabled ? [
      {
        Sid      = "DeleteOnlyLegacyMediaVersions"
        Effect   = "Allow"
        Action   = local.legacy_media_purge_deletion_actions
        Resource = [for prefix in local.legacy_media_purge_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
        Condition = {
          DateLessThan = { "aws:CurrentTime" = var.legacy_media_purge_permission_expires_at }
        }
      }
    ] : [])
  }
  legacy_media_purge_policy_json = jsonencode(local.legacy_media_purge_policy)
}

resource "aws_iam_policy" "legacy_media_purge_boundary" {
  count = var.legacy_media_purge_identity_enabled ? 1 : 0

  name        = "${local.legacy_media_purge_user_name}-boundary"
  description = "Maximum permissions for the temporary one-time legacy media purge identity"
  policy      = local.legacy_media_purge_policy_json
  tags        = local.tags
}

resource "aws_iam_user" "legacy_media_purge" {
  count = var.legacy_media_purge_identity_enabled ? 1 : 0

  name                 = local.legacy_media_purge_user_name
  permissions_boundary = aws_iam_policy.legacy_media_purge_boundary[0].arn
  tags                 = local.tags
}

resource "aws_iam_user_policy" "legacy_media_purge" {
  count = var.legacy_media_purge_identity_enabled ? 1 : 0

  name   = "legacy-media-purge"
  user   = aws_iam_user.legacy_media_purge[0].name
  policy = local.legacy_media_purge_policy_json

  # Install the deny fence before granting version deletion. On teardown this
  # dependency removes deletion permission before the fence can be removed.
  depends_on = [aws_s3_bucket_policy.repairs]
}

output "legacy_media_purge_user_name" {
  description = "Temporary purge IAM user created only by the explicit gate. Terraform creates no access key."
  value       = try(aws_iam_user.legacy_media_purge[0].name, null)
}

output "legacy_media_purge_user_arn" {
  description = "Exact temporary purge principal ARN that the protected host wrapper must verify."
  value       = try(aws_iam_user.legacy_media_purge[0].arn, null)
}

locals {
  media_cleanup_user_name      = "${local.name}-media-cleanup"
  media_cleanup_prefixes       = ["repairs/*", "equipment/*", "profiles/*"]
  media_cleanup_list_actions   = ["s3:ListBucketVersions"]
  media_cleanup_delete_actions = ["s3:DeleteObjectVersion"]
  media_cleanup_deny_actions   = ["s3:DeleteObject", "s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:PutObjectAcl", "s3:PutObjectTagging", "sts:AssumeRole"]
}

resource "aws_iam_user" "media_cleanup" {
  name = local.media_cleanup_user_name
  tags = local.tags
}

resource "aws_iam_access_key" "media_cleanup" {
  user = aws_iam_user.media_cleanup.name
}

data "aws_iam_policy_document" "media_cleanup" {
  statement {
    sid       = "ListOnlyMediaVersions"
    effect    = "Allow"
    actions   = local.media_cleanup_list_actions
    resources = [aws_s3_bucket.repairs.arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = local.media_cleanup_prefixes
    }
  }

  statement {
    sid       = "DeleteOnlyQueuedMediaVersions"
    effect    = "Allow"
    actions   = local.media_cleanup_delete_actions
    resources = [for prefix in local.media_cleanup_prefixes : "${aws_s3_bucket.repairs.arn}/${prefix}"]
  }

  statement {
    sid       = "DenyWritesAndRoleChaining"
    effect    = "Deny"
    actions   = local.media_cleanup_deny_actions
    resources = ["*"]
  }
}

resource "aws_iam_user_policy" "media_cleanup" {
  name   = "media-cleanup"
  user   = aws_iam_user.media_cleanup.name
  policy = data.aws_iam_policy_document.media_cleanup.json
}

output "media_cleanup_access_key_id" {
  value     = aws_iam_access_key.media_cleanup.id
  sensitive = true
}

output "media_cleanup_secret_access_key" {
  value     = aws_iam_access_key.media_cleanup.secret
  sensitive = true
}

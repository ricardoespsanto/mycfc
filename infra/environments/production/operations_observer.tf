locals {
  operations_observer_name = "${local.name}-operations-observer"
  operations_observer_log_actions = [
    "logs:DescribeLogStreams",
    "logs:FilterLogEvents",
    "logs:GetLogEvents",
  ]
  operations_observer_ecr_actions = [
    "ecr:DescribeImages",
    "ecr:ListImages",
  ]
  operations_observer_alarm_actions = [
    "cloudwatch:DescribeAlarms",
    "cloudwatch:GetMetricData",
    "cloudwatch:GetMetricStatistics",
    "cloudwatch:ListMetrics",
  ]
  operations_observer_deny_actions = [
    "ecr:BatchDeleteImage", "ecr:CompleteLayerUpload", "ecr:DeleteRepository*", "ecr:InitiateLayerUpload", "ecr:PutImage", "ecr:PutImageTagMutability", "ecr:UploadLayerPart",
    "logs:CreateLogGroup", "logs:CreateLogStream", "logs:Delete*", "logs:PutLogEvents", "logs:PutMetricFilter",
    "s3:GetObject*", "s3:ListBucket*", "secretsmanager:GetSecretValue", "ssm:GetParameter*", "sts:AssumeRole",
  ]
  operations_observer_trust_statements = concat(
    length(var.operations_observer_principal_arns) == 0 ? [] : [{
      Sid       = "TrustedHumanPrincipals"
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { AWS = var.operations_observer_principal_arns }
    }],
    var.operations_observer_github_oidc_provider_arn == null ? [] : [{
      Sid       = "VerifiedReleaseWorkflow"
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.operations_observer_github_oidc_provider_arn }
      Condition = { StringEquals = {
        "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        "token.actions.githubusercontent.com:sub" = "repo:${var.github_org}/${var.github_repo}:environment:${var.github_environment}"
      } }
    }],
  )
  operations_observer_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ReadDeploymentEvidence"
        Effect   = "Allow"
        Action   = local.operations_observer_log_actions
        Resource = [aws_cloudwatch_log_group.deployment.arn, "${aws_cloudwatch_log_group.deployment.arn}:*"]
      },
      {
        Sid      = "ReadReleaseInventory"
        Effect   = "Allow"
        Action   = local.operations_observer_ecr_actions
        Resource = aws_ecr_repository.app.arn
      },
      {
        Sid      = "ReadDeploymentAlarms"
        Effect   = "Allow"
        Action   = local.operations_observer_alarm_actions
        Resource = "*"
      },
      {
        Sid      = "DenySecretsStateAndMutation"
        Effect   = "Deny"
        Action   = local.operations_observer_deny_actions
        Resource = "*"
      },
    ]
  })
}

resource "aws_iam_role" "operations_observer" {
  count = var.operations_observer_enabled ? 1 : 0

  name = local.operations_observer_name
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = local.operations_observer_trust_statements
  })
  permissions_boundary = aws_iam_policy.operations_observer_boundary[0].arn
  max_session_duration = 3600
  tags                 = local.tags
}

resource "aws_iam_policy" "operations_observer_boundary" {
  count = var.operations_observer_enabled ? 1 : 0

  name        = "${local.operations_observer_name}-boundary"
  description = "Maximum read-only deployment evidence permissions for the short-lived MyCFC observer."
  policy      = local.operations_observer_policy
  tags        = local.tags
}

resource "aws_iam_role_policy" "operations_observer" {
  count = var.operations_observer_enabled ? 1 : 0

  name   = "operations-observer"
  role   = aws_iam_role.operations_observer[0].id
  policy = local.operations_observer_policy
}

output "operations_observer_role_arn" {
  value = var.operations_observer_enabled ? aws_iam_role.operations_observer[0].arn : null
}

resource "aws_iam_openid_connect_provider" "github_actions" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = []
}

data "aws_iam_policy_document" "github_actions_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github_actions.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_org}/${var.github_repo}:environment:${var.github_environment}"]
    }
  }
}

resource "aws_iam_role" "github_infra_plan" {
  name               = "github-infra-plan"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume_role.json
}

resource "aws_iam_role_policy_attachment" "github_infra_plan_read_only" {
  role       = aws_iam_role.github_infra_plan.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

variable "state_bucket_name" {
  type = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.state_bucket_name)) && !can(regex("(^|\\.)[0-9]{1,3}(\\.[0-9]{1,3}){3}($|\\.)", var.state_bucket_name))
    error_message = "state_bucket_name must be a valid non-IP-address S3 bucket name of 3-63 lowercase characters."
  }
}

data "aws_iam_policy_document" "github_infra_plan_state" {
  statement {
    sid       = "ReadTerraformState"
    effect    = "Allow"
    actions   = ["s3:GetObject", "s3:GetObjectVersion"]
    resources = ["arn:aws:s3:::${var.state_bucket_name}/mycfc/production/terraform.tfstate"]
  }

  statement {
    sid       = "LockTerraformState"
    effect    = "Allow"
    actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
    resources = ["arn:aws:s3:::${var.state_bucket_name}/mycfc/production/terraform.tfstate.tflock"]
  }

  statement {
    sid       = "DiscoverTerraformState"
    effect    = "Allow"
    actions   = ["s3:GetBucketLocation", "s3:GetBucketVersioning", "s3:ListBucket"]
    resources = ["arn:aws:s3:::${var.state_bucket_name}"]
  }
}

resource "aws_iam_role_policy" "github_infra_plan_state" {
  name   = "terraform-state"
  role   = aws_iam_role.github_infra_plan.name
  policy = data.aws_iam_policy_document.github_infra_plan_state.json
}

resource "aws_iam_role" "github_infra_apply" {
  name               = "github-infra-apply"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume_role.json
}

data "aws_iam_policy_document" "github_infra_apply" {
  statement {
    sid    = "ReadInfrastructureInventory"
    effect = "Allow"
    actions = [
      "acm:ListCertificates",
      "application-autoscaling:DescribeScalableTargets", "application-autoscaling:DescribeScalingPolicies",
      "cloudwatch:DescribeAlarms",
      "ec2:DescribeAvailabilityZones", "ec2:DescribeInternetGateways", "ec2:DescribeRouteTables", "ec2:DescribeSecurityGroupRules", "ec2:DescribeSecurityGroups", "ec2:DescribeSubnets", "ec2:DescribeVpcAttribute", "ec2:DescribeVpcEndpoints", "ec2:DescribeVpcs",
      "elasticloadbalancing:DescribeListeners", "elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeTargetGroups",
      "rds:DescribeDBInstances", "rds:DescribeDBParameterGroups", "rds:DescribeDBSubnetGroups",
      "route53:ListHostedZones",
      "s3:ListAllMyBuckets",
      "wafv2:ListWebACLs",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "ReadDeclaredResources"
    effect = "Allow"
    actions = [
      "acm:DescribeCertificate", "acm:ListTagsForCertificate",
      "ecr:DescribeRepositories", "ecr:GetLifecyclePolicy",
      "ecs:DescribeClusters", "ecs:DescribeServices", "ecs:DescribeTaskDefinition", "ecs:ListTagsForResource",
      "iam:GetOpenIDConnectProvider", "iam:GetRole", "iam:ListAttachedRolePolicies", "iam:ListRolePolicies",
      "route53:ListResourceRecordSets",
      "wafv2:GetWebACL",
    ]
    resources = [
      "arn:aws:acm:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:certificate/*",
      aws_ecr_repository.app.arn,
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:cluster/${local.name}",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:service/${local.name}/${local.name}-app",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:task-definition/${var.project_name}-app:*",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:task-definition/${var.project_name}-migrate:*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/token.actions.githubusercontent.com",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-infra-plan",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-infra-apply",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-deploy",
      aws_iam_role.app_execution.arn,
      aws_iam_role.app_task.arn,
      aws_iam_role.migrate_execution.arn,
      aws_iam_role.migrate_task.arn,
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${local.name}-rds-monitoring",
      "arn:aws:route53:::hostedzone/${var.route53_zone_id}",
      "arn:aws:wafv2:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:regional/webacl/${local.name}-web-acl/*",
    ]
  }

  statement {
    sid       = "ManageTerraformState"
    effect    = "Allow"
    actions   = ["s3:GetBucketLocation", "s3:GetBucketVersioning", "s3:ListBucket"]
    resources = ["arn:aws:s3:::${var.state_bucket_name}"]
  }

  statement {
    sid     = "ManageTerraformStateObjects"
    effect  = "Allow"
    actions = ["s3:DeleteObject", "s3:GetObject", "s3:GetObjectVersion", "s3:PutObject"]
    resources = [
      "arn:aws:s3:::${var.state_bucket_name}/mycfc/production/terraform.tfstate",
      "arn:aws:s3:::${var.state_bucket_name}/mycfc/production/terraform.tfstate.tflock",
    ]
  }

  statement {
    sid    = "ManageApplicationBuckets"
    effect = "Allow"
    actions = [
      "s3:CreateBucket", "s3:DeleteBucket", "s3:DeleteBucketPolicy", "s3:DeleteBucketOwnershipControls", "s3:DeleteBucketPublicAccessBlock", "s3:DeleteBucketEncryption", "s3:DeleteBucketLifecycle", "s3:DeleteBucketVersioning",
      "s3:GetBucketEncryption", "s3:GetBucketLifecycleConfiguration", "s3:GetBucketOwnershipControls", "s3:GetBucketPolicy", "s3:GetBucketPublicAccessBlock", "s3:GetBucketTagging", "s3:GetBucketVersioning", "s3:ListBucket",
      "s3:PutBucketEncryption", "s3:PutBucketLifecycleConfiguration", "s3:PutBucketOwnershipControls", "s3:PutBucketPolicy", "s3:PutBucketPublicAccessBlock", "s3:PutBucketTagging", "s3:PutBucketVersioning",
    ]
    resources = [
      "arn:aws:s3:::${local.repair_bucket}",
      "arn:aws:s3:::${local.log_bucket}",
    ]
  }

  statement {
    sid    = "ManageNetwork"
    effect = "Allow"
    actions = [
      "ec2:AssociateRouteTable", "ec2:AttachInternetGateway", "ec2:AuthorizeSecurityGroupEgress", "ec2:AuthorizeSecurityGroupIngress", "ec2:CreateInternetGateway", "ec2:CreateRoute", "ec2:CreateRouteTable", "ec2:CreateSecurityGroup", "ec2:CreateSubnet", "ec2:CreateTags", "ec2:CreateVpc", "ec2:CreateVpcEndpoint",
      "ec2:DeleteInternetGateway", "ec2:DeleteRoute", "ec2:DeleteRouteTable", "ec2:DeleteSecurityGroup", "ec2:DeleteSubnet", "ec2:DeleteVpc", "ec2:DeleteVpcEndpoints", "ec2:DetachInternetGateway", "ec2:DisassociateRouteTable", "ec2:ModifySubnetAttribute", "ec2:ModifyVpcAttribute", "ec2:ModifyVpcEndpoint", "ec2:RevokeSecurityGroupEgress", "ec2:RevokeSecurityGroupIngress",
    ]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "aws:RequestedRegion"
      values   = [var.aws_region]
    }
  }

  statement {
    sid    = "ManageApplicationRepository"
    effect = "Allow"
    actions = [
      "ecr:CreateRepository", "ecr:DeleteLifecyclePolicy", "ecr:DeleteRepository", "ecr:PutImageScanningConfiguration", "ecr:PutImageTagMutability", "ecr:PutLifecyclePolicy", "ecr:TagResource", "ecr:UntagResource",
    ]
    resources = [aws_ecr_repository.app.arn]
  }

  statement {
    sid    = "ManageDatabase"
    effect = "Allow"
    actions = [
      "rds:AddTagsToResource", "rds:CreateDBInstance", "rds:CreateDBParameterGroup", "rds:CreateDBSubnetGroup", "rds:DeleteDBInstance", "rds:DeleteDBParameterGroup", "rds:DeleteDBSubnetGroup", "rds:ModifyDBInstance", "rds:ModifyDBParameterGroup", "rds:ModifyDBSubnetGroup", "rds:RemoveTagsFromResource",
    ]
    resources = [
      "arn:aws:rds:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:db:${local.name}",
      "arn:aws:rds:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:pg:${local.name}-postgres16",
      "arn:aws:rds:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:subgrp:${local.name}",
    ]
  }

  statement {
    sid       = "ManageCertificate"
    effect    = "Allow"
    actions   = ["acm:AddTagsToCertificate", "acm:DeleteCertificate", "acm:RequestCertificate"]
    resources = ["arn:aws:acm:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:certificate/*"]
  }

  statement {
    sid       = "ManageApplicationDNS"
    effect    = "Allow"
    actions   = ["route53:ChangeResourceRecordSets", "route53:GetHostedZone"]
    resources = ["arn:aws:route53:::hostedzone/${var.route53_zone_id}"]
  }

  statement {
    sid    = "ManageLoadBalancer"
    effect = "Allow"
    actions = [
      "elasticloadbalancing:AddTags", "elasticloadbalancing:CreateListener", "elasticloadbalancing:CreateLoadBalancer", "elasticloadbalancing:CreateTargetGroup", "elasticloadbalancing:DeleteListener", "elasticloadbalancing:DeleteLoadBalancer", "elasticloadbalancing:DeleteTargetGroup", "elasticloadbalancing:ModifyListener", "elasticloadbalancing:ModifyLoadBalancerAttributes", "elasticloadbalancing:ModifyTargetGroup", "elasticloadbalancing:ModifyTargetGroupAttributes", "elasticloadbalancing:RemoveTags",
    ]
    resources = [
      "arn:aws:elasticloadbalancing:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:loadbalancer/app/${local.name}-alb/*",
      "arn:aws:elasticloadbalancing:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:listener/app/${local.name}-alb/*",
      "arn:aws:elasticloadbalancing:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:targetgroup/${local.name}-app/*",
    ]
  }

  statement {
    sid       = "ManageWebACL"
    effect    = "Allow"
    actions   = ["wafv2:AssociateWebACL", "wafv2:CreateWebACL", "wafv2:DeleteWebACL", "wafv2:DisassociateWebACL", "wafv2:TagResource", "wafv2:UntagResource", "wafv2:UpdateWebACL"]
    resources = ["arn:aws:wafv2:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:regional/webacl/${local.name}-web-acl/*"]
  }

  statement {
    sid       = "ManageApplicationLogGroup"
    effect    = "Allow"
    actions   = ["logs:CreateLogGroup", "logs:DeleteLogGroup", "logs:ListTagsForResource", "logs:PutRetentionPolicy", "logs:TagResource", "logs:UntagResource"]
    resources = ["arn:aws:logs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:log-group:/ecs/${local.name}:*"]
  }

  statement {
    sid    = "ManageApplicationECS"
    effect = "Allow"
    actions = [
      "ecs:CreateCluster", "ecs:CreateService", "ecs:DeleteCluster", "ecs:DeleteService", "ecs:DeregisterTaskDefinition", "ecs:RegisterTaskDefinition", "ecs:TagResource", "ecs:UntagResource", "ecs:UpdateClusterSettings", "ecs:UpdateService",
    ]
    resources = [
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:cluster/${local.name}",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:service/${local.name}/${local.name}-app",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:task-definition/${var.project_name}-app:*",
      "arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:task-definition/${var.project_name}-migrate:*",
    ]
  }

  statement {
    sid       = "PassDeclaredTaskRoles"
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.app_execution.arn, aws_iam_role.app_task.arn, aws_iam_role.migrate_execution.arn, aws_iam_role.migrate_task.arn]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  statement {
    sid       = "CreateApplicationSecrets"
    effect    = "Allow"
    actions   = ["secretsmanager:CreateSecret"]
    resources = ["*"]
  }

  statement {
    sid       = "ManageApplicationSecrets"
    effect    = "Allow"
    actions   = ["secretsmanager:DeleteSecret", "secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue", "secretsmanager:ListSecretVersionIds", "secretsmanager:PutSecretValue", "secretsmanager:TagResource", "secretsmanager:UntagResource", "secretsmanager:UpdateSecret"]
    resources = [aws_secretsmanager_secret.app_db_password.arn, aws_secretsmanager_secret.csrf_auth_key.arn]
  }

  statement {
    sid       = "ManageApplicationScaling"
    effect    = "Allow"
    actions   = ["application-autoscaling:DeleteScalingPolicy", "application-autoscaling:DeregisterScalableTarget", "application-autoscaling:PutScalingPolicy", "application-autoscaling:RegisterScalableTarget"]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "application-autoscaling:service-namespace"
      values   = ["ecs"]
    }
  }

  statement {
    sid    = "ManageInfrastructureIAM"
    effect = "Allow"
    actions = [
      "iam:AttachRolePolicy", "iam:CreateOpenIDConnectProvider", "iam:CreateRole", "iam:DeleteOpenIDConnectProvider", "iam:DeleteRole", "iam:DeleteRolePolicy", "iam:DetachRolePolicy", "iam:PutRolePolicy", "iam:TagOpenIDConnectProvider", "iam:TagRole", "iam:UntagOpenIDConnectProvider", "iam:UntagRole", "iam:UpdateAssumeRolePolicy",
    ]
    resources = [
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/token.actions.githubusercontent.com",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-infra-plan",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-infra-apply",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/github-deploy",
      aws_iam_role.app_execution.arn,
      aws_iam_role.app_task.arn,
      aws_iam_role.migrate_execution.arn,
      aws_iam_role.migrate_task.arn,
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${local.name}-rds-monitoring",
    ]
  }
}

resource "aws_iam_role_policy" "github_infra_apply" {
  name   = "infrastructure-apply"
  role   = aws_iam_role.github_infra_apply.name
  policy = data.aws_iam_policy_document.github_infra_apply.json
}

resource "aws_iam_role" "github_deploy" {
  name               = "github-deploy"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume_role.json
}

data "aws_iam_policy_document" "github_deploy" {
  statement {
    sid       = "GetECRAuthorizationToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "PushApplicationImage"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:CompleteLayerUpload",
      "ecr:DescribeImages",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
    ]
    resources = [aws_ecr_repository.app.arn]
  }

  statement {
    sid       = "DescribeApplicationCluster"
    effect    = "Allow"
    actions   = ["ecs:DescribeClusters"]
    resources = [aws_ecs_cluster.app.arn]
  }

  statement {
    sid       = "UpdateApplicationService"
    effect    = "Allow"
    actions   = ["ecs:DescribeServices", "ecs:UpdateService"]
    resources = [aws_ecs_service.app.id]
  }

  statement {
    sid     = "ManageApplicationTaskDefinitions"
    effect  = "Allow"
    actions = ["ecs:DescribeTaskDefinition"]
    resources = [
      "${aws_ecs_task_definition.app.arn_without_revision}:*",
      "${aws_ecs_task_definition.migrate.arn_without_revision}:*",
    ]
  }

  statement {
    sid       = "RegisterApplicationTaskDefinitions"
    effect    = "Allow"
    actions   = ["ecs:RegisterTaskDefinition"]
    resources = ["*"]

    condition {
      test     = "StringLike"
      variable = "ecs:TaskDefinitionFamily"
      values   = [aws_ecs_task_definition.app.family, aws_ecs_task_definition.migrate.family]
    }
  }

  statement {
    sid     = "RunApplicationTasks"
    effect  = "Allow"
    actions = ["ecs:RunTask"]
    resources = [
      "${aws_ecs_task_definition.app.arn_without_revision}:*",
      "${aws_ecs_task_definition.migrate.arn_without_revision}:*",
    ]

    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.app.arn]
    }
  }

  statement {
    sid       = "DescribeApplicationTasks"
    effect    = "Allow"
    actions   = ["ecs:DescribeTasks"]
    resources = ["arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:task/${aws_ecs_cluster.app.name}/*"]
  }

  statement {
    sid     = "PassApplicationTaskRoles"
    effect  = "Allow"
    actions = ["iam:PassRole"]
    resources = [
      aws_iam_role.app_execution.arn,
      aws_iam_role.app_task.arn,
      aws_iam_role.migrate_execution.arn,
      aws_iam_role.migrate_task.arn,
    ]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "github_deploy" {
  name   = "deploy"
  role   = aws_iam_role.github_deploy.name
  policy = data.aws_iam_policy_document.github_deploy.json
}

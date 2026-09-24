variable "aws_region" {
  type        = string
  description = "Region used in the ephemeral QA bucket namespace."
  default     = "eu-west-1"

  validation {
    condition     = can(regex("^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$", var.aws_region))
    error_message = "aws_region must be an AWS region identifier."
  }
}

variable "github_oidc_provider_arn" {
  type        = string
  description = "Existing GitHub Actions OIDC provider in this AWS account; this stack does not create it."

  validation {
    condition     = can(regex("^arn:aws:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.github_oidc_provider_arn))
    error_message = "Supply the exact GitHub Actions OIDC provider ARN."
  }
}

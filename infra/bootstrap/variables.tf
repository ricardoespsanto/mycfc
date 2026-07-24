variable "aws_region" {
  type        = string
  description = "AWS region that will contain the Terraform state bucket."
  default     = "eu-west-1"

  validation {
    condition     = can(regex("^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$", var.aws_region))
    error_message = "aws_region must be an AWS region identifier."
  }
}

variable "project_name" {
  type        = string
  description = "Project identifier used in resource names and tags."
  default     = "mycfc"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}$", var.project_name))
    error_message = "project_name must be 2-31 lowercase letters, digits, or hyphens and begin with a letter."
  }
}

variable "state_bucket_name" {
  type        = string
  description = "Globally unique S3 bucket name for Terraform state."

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.state_bucket_name)) && !can(regex("(^|\\.)[0-9]{1,3}(\\.[0-9]{1,3}){3}($|\\.)", var.state_bucket_name))
    error_message = "state_bucket_name must be a valid non-IP-address S3 bucket name of 3-63 lowercase characters."
  }
}

variable "repository" {
  type        = string
  description = "Source repository identifier, for example organisation/repository."

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.repository))
    error_message = "repository must be an organisation/repository identifier."
  }
}

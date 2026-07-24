variable "aws_region" {
  type    = string
  default = "eu-west-1"
}
variable "project_name" {
  type    = string
  default = "mycfc"
}
variable "environment" {
  type    = string
  default = "production"
}
variable "domain_name" {
  type    = string
  default = "mycfc.pt"
}
variable "route53_zone_id" { type = string }
variable "github_org" { type = string }
variable "github_repo" { type = string }
variable "github_environment" {
  type    = string
  default = "production"
}
variable "calendar_competition_id" {
  type      = string
  sensitive = true
}
variable "calendar_training_id" {
  type      = string
  sensitive = true
}
variable "calendar_social_id" {
  type      = string
  sensitive = true
}
variable "calendar_cleanups_id" {
  type      = string
  sensitive = true
}
variable "google_calendar_api_key" {
  type      = string
  sensitive = true
}
variable "gallery_url" { type = string }
variable "consent_terms_version" { type = string }
variable "consent_terms_sha256" { type = string }
variable "consent_image_version" { type = string }
variable "consent_image_sha256" { type = string }
variable "consent_minor_version" { type = string }
variable "consent_minor_sha256" { type = string }
variable "image_digest" { type = string }
variable "vpc_cidr" {
  type    = string
  default = "10.42.0.0/16"
}
variable "task_cpu" {
  type    = number
  default = 512
}
variable "task_memory" {
  type    = number
  default = 1024
}
variable "db_instance_class" {
  type    = string
  default = "db.t4g.micro"
}
variable "db_connection_budget" {
  type    = number
  default = 64
}
variable "alarm_email" {
  type     = string
  default  = null
  nullable = true
}

check "production_input_validation" {
  assert {
    condition     = var.environment == "production" && can(regex("^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$", var.aws_region)) && can(regex("^[a-z][a-z0-9-]{1,30}$", var.project_name)) && can(regex("^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$", var.domain_name))
    error_message = "Production environment, AWS region, project name, or domain name is invalid."
  }
  assert {
    condition     = can(regex("^Z[A-Z0-9]+$", var.route53_zone_id)) && can(regex("^[A-Za-z0-9-]+$", var.github_org)) && can(regex("^[A-Za-z0-9_.-]+$", var.github_repo)) && can(regex("^[A-Za-z0-9_.-]+$", var.github_environment))
    error_message = "Route 53 zone and GitHub organisation, repository, and environment values are invalid."
  }
  assert {
    condition     = alltrue([for value in [var.calendar_competition_id, var.calendar_training_id, var.calendar_social_id, var.calendar_cleanups_id, var.google_calendar_api_key, var.consent_terms_version, var.consent_image_version, var.consent_minor_version] : trimspace(value) != ""])
    error_message = "Calendar IDs, Google API key, and consent versions are required."
  }
  assert {
    condition     = can(regex("^https://", var.gallery_url)) && alltrue([for value in [var.consent_terms_sha256, var.consent_image_sha256, var.consent_minor_sha256] : can(regex("^[0-9a-f]{64}$", value))])
    error_message = "gallery_url must be HTTPS and consent hashes must be lowercase SHA-256 digests."
  }
  assert {
    condition     = can(regex("^sha256:[0-9a-f]{64}$", var.image_digest)) && can(cidrnetmask(var.vpc_cidr)) && contains([256, 512, 1024, 2048, 4096], var.task_cpu) && var.task_memory >= 512 && var.db_connection_budget >= 56
    error_message = "Image digest, VPC CIDR, Fargate sizing, or database connection budget is invalid."
  }
}

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
variable "state_bucket_name" {
  type = string
}
variable "domain_name" {
  type    = string
  default = "mycfc.pt"
}
variable "ses_from_local_part" {
  type        = string
  default     = "no-reply"
  description = "Local part of the only address the application SMTP identity may send from."

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$", var.ses_from_local_part))
    error_message = "ses_from_local_part must be a valid lowercase email local part."
  }
}
variable "ses_mail_from_subdomain" {
  type        = string
  default     = "mail"
  description = "Subdomain used as the custom SES MAIL FROM domain."

  validation {
    condition     = can(regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$", var.ses_mail_from_subdomain))
    error_message = "ses_mail_from_subdomain must be a valid DNS label."
  }
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
variable "consent_terms_url" { type = string }
variable "consent_image_url" { type = string }
variable "consent_minor_url" { type = string }
variable "image_git_sha" { type = string }
variable "database_host" {
  type    = string
  default = "postgres"
}
variable "database_port" {
  type    = number
  default = 5432
}
variable "database_sslmode" {
  type    = string
  default = "disable"
}
variable "postgres_username" {
  type    = string
  default = "mycfc"
}
variable "postgres_password" {
  type      = string
  sensitive = true
}
variable "app_db_username" { type = string }
variable "app_db_password" {
  type      = string
  sensitive = true
}
variable "migration_db_username" { type = string }
variable "migration_db_password" {
  type      = string
  sensitive = true
}
variable "database_name" {
  type    = string
  default = "mycfc"
}
variable "turnstile_site_key" { type = string }
variable "turnstile_secret_key" {
  type      = string
  sensitive = true
}
variable "smtp_port" {
  type    = number
  default = 587
}
variable "smtp_from_name" {
  type    = string
  default = "MyCFC"
}
variable "smtp_tls_mode" {
  type    = string
  default = "starttls"
}
variable "smtp_timeout" {
  type    = string
  default = "10s"
}
variable "log_level" {
  type    = string
  default = "INFO"
}
variable "trusted_proxy_cidrs" {
  type    = list(string)
  default = ["172.30.0.0/24"]
}
variable "db_max_conns" {
  type    = number
  default = 8
}
variable "db_min_conns" {
  type    = number
  default = 1
}
variable "db_max_conn_lifetime" {
  type    = string
  default = "30m"
}
variable "db_max_conn_idle_time" {
  type    = string
  default = "5m"
}
variable "db_health_check_period" {
  type    = string
  default = "30s"
}
variable "session_lifetime" {
  type    = string
  default = "12h"
}
variable "session_idle_timeout" {
  type    = string
  default = "30m"
}
variable "max_request_bytes" {
  type    = number
  default = 12582912
}
variable "max_photo_bytes" {
  type    = number
  default = 10485760
}
variable "http_read_header_timeout" {
  type    = string
  default = "5s"
}
variable "http_read_timeout" {
  type    = string
  default = "15s"
}
variable "http_write_timeout" {
  type    = string
  default = "30s"
}
variable "http_idle_timeout" {
  type    = string
  default = "60s"
}
variable "shutdown_timeout" {
  type    = string
  default = "20s"
}
variable "release_check_timeout" {
  type    = string
  default = "3s"
}
variable "release_check_cache_ttl" {
  type    = string
  default = "15m"
}
variable "release_agent_cutover_complete" {
  type        = bool
  default     = false
  description = "Set true only after the dedicated release profile and updated scripts have been verified on the production host."
}
variable "alb_log_retention_days" {
  type    = number
  default = 90
}
variable "waf_login_rate_limit" {
  type    = number
  default = 100
}
variable "waf_general_rate_limit" {
  type    = number
  default = 2000
}
variable "alb_requests_per_target" {
  type    = number
  default = 1000
}
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

  validation {
    condition     = var.alarm_email == null || can(regex("^[^@[:space:]]+@[^@[:space:]]+\\.[^@[:space:]]+$", var.alarm_email))
    error_message = "alarm_email must be null or a valid email address."
  }
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
    condition     = can(regex("^sha256:[0-9a-f]{64}$", var.image_digest)) && can(cidrnetmask(var.vpc_cidr)) && contains(lookup(local.fargate_task_memory_by_cpu, tostring(var.task_cpu), []), var.task_memory) && var.db_connection_budget >= 56
    error_message = "Image digest, VPC CIDR, Fargate sizing, or database connection budget is invalid."
  }
  assert {
    condition     = can(regex("^[0-9a-f]{40}$", var.image_git_sha)) && alltrue([for value in [var.consent_terms_url, var.consent_image_url, var.consent_minor_url] : can(regex("^https://", value))])
    error_message = "Image Git SHA and consent document URLs are invalid."
  }
  assert {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,62}$", var.app_db_username)) && can(regex("^[A-Za-z][A-Za-z0-9_]{0,62}$", var.migration_db_username)) && var.app_db_username != var.migration_db_username && can(regex("^[A-Za-z][A-Za-z0-9_]{0,62}$", var.database_name))
    error_message = "App and migration database users must be distinct PostgreSQL identifiers, as must the database name."
  }
  assert {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,62}$", var.postgres_username)) && trimspace(var.postgres_password) != "" && trimspace(var.app_db_password) != "" && trimspace(var.migration_db_password) != "" && !contains([var.app_db_username, var.migration_db_username], var.postgres_username)
    error_message = "Bootstrap, app, and migration database credentials must be non-empty and use distinct PostgreSQL role names."
  }
  assert {
    condition     = trimspace(var.database_host) != "" && var.database_port >= 1 && var.database_port <= 65535 && contains(["disable", "allow", "prefer", "require", "verify-ca", "verify-full"], var.database_sslmode)
    error_message = "Database host, port, or sslmode is invalid."
  }
  assert {
    condition     = trimspace(var.turnstile_site_key) != "" && trimspace(var.turnstile_secret_key) != "" && var.smtp_port >= 1 && var.smtp_port <= 65535 && contains(["starttls", "implicit"], var.smtp_tls_mode) && contains(["DEBUG", "INFO", "WARN", "ERROR"], upper(var.log_level))
    error_message = "Turnstile, SMTP, or log-level runtime configuration is invalid."
  }
  assert {
    condition     = alltrue([for value in var.trusted_proxy_cidrs : can(cidrnetmask(value))]) && var.db_max_conns >= 1 && var.db_min_conns >= 0 && var.db_min_conns <= var.db_max_conns && var.max_photo_bytes > 0 && var.max_request_bytes > var.max_photo_bytes
    error_message = "Runtime CIDRs, database pool sizing, or upload/request byte limits are invalid."
  }
  assert {
    condition     = var.alb_log_retention_days >= 30 && var.waf_login_rate_limit >= 10 && var.waf_general_rate_limit >= var.waf_login_rate_limit && var.alb_requests_per_target > 0
    error_message = "ALB log retention and WAF/autoscaling thresholds are invalid."
  }
}

variable "project_name" {
  type    = string
  default = "mycfc"
}

variable "environment" {
  type    = string
  default = "production"
}

variable "location" {
  type    = string
  default = "fsn1"

  validation {
    condition     = contains(["fsn1", "nbg1"], var.location)
    error_message = "location must be fsn1 or nbg1."
  }
}

variable "ssh_public_key" {
  type = string

  validation {
    condition     = can(regex("^ssh-(ed25519|rsa|ecdsa) ", var.ssh_public_key))
    error_message = "ssh_public_key must be a valid OpenSSH public key."
  }
}

variable "deploy_ssh_public_key" {
  description = "OpenSSH public key used by the deployment operator."
  type        = string
}

variable "ssh_source_ips" {
  type        = list(string)
  default     = []
  description = "CIDRs allowed to connect to SSH. An empty list leaves port 22 closed."

  validation {
    condition     = alltrue([for cidr in var.ssh_source_ips : can(cidrnetmask(cidr))])
    error_message = "ssh_source_ips must contain valid CIDR ranges."
  }
}

variable "privacy_restore_infrastructure_enabled" {
  description = "Provision the inert, restore-independent encrypted tombstone ledger. This does not create credentials or enable reads/writes."
  type        = bool
  default     = false
}

variable "privacy_restore_ledger_write_enabled" {
  description = "Grant the isolated ledger writer permission to append and verify tombstones. Requires separately approved infrastructure."
  type        = bool
  default     = false

  validation {
    condition     = !var.privacy_restore_ledger_write_enabled || var.privacy_restore_infrastructure_enabled
    error_message = "privacy_restore_ledger_write_enabled requires privacy_restore_infrastructure_enabled."
  }
}

variable "privacy_restore_ledger_replay_enabled" {
  description = "Grant the isolated restore role permission to read tombstones during an offline restore."
  type        = bool
  default     = false

  validation {
    condition     = !var.privacy_restore_ledger_replay_enabled || var.privacy_restore_infrastructure_enabled
    error_message = "privacy_restore_ledger_replay_enabled requires privacy_restore_infrastructure_enabled."
  }
}

variable "postgres_backup_cleanup_identity_enabled" {
  description = "Provision an inert cleanup-only IAM identity with version-inventory permission. Terraform creates no access key and grants no deletion."
  type        = bool
  default     = false
}

variable "postgres_backup_noncurrent_cleanup_enabled" {
  description = "Grant exact-version deletion to the separate cleanup identity and configure lifecycle as a best-effort backstop. Requires a separately reviewed destructive plan."
  type        = bool
  default     = false

  validation {
    condition     = !var.postgres_backup_noncurrent_cleanup_enabled || var.postgres_backup_cleanup_identity_enabled
    error_message = "postgres_backup_noncurrent_cleanup_enabled requires postgres_backup_cleanup_identity_enabled."
  }
}

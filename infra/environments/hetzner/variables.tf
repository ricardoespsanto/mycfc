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

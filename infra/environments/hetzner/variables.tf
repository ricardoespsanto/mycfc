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

variable "cloudflare_proxy_cidrs" {
  type = list(string)
  default = [
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
    "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
    "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
    "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22", "2400:cb00::/32",
    "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
    "2a06:98c0::/29", "2c0f:f248::/32",
  ]
  description = "Cloudflare's published IPv4 and IPv6 proxy CIDRs allowed to reach the origin."

  validation {
    condition     = length(var.cloudflare_proxy_cidrs) > 0 && alltrue([for cidr in var.cloudflare_proxy_cidrs : can(cidrhost(cidr, 0))])
    error_message = "cloudflare_proxy_cidrs must be a non-empty list of valid CIDR ranges."
  }
}

locals {
  name = "${var.project_name}-${var.environment}"
}

resource "hcloud_ssh_key" "operator" {
  name       = "${local.name}-operator"
  public_key = var.ssh_public_key
}

resource "hcloud_ssh_key" "deploy" {
  name       = "${local.name}-deploy"
  public_key = var.deploy_ssh_public_key
}

resource "hcloud_firewall" "server" {
  name = "${local.name}-server"

  dynamic "rule" {
    for_each = length(var.ssh_source_ips) > 0 ? [1] : []

    content {
      direction  = "in"
      protocol   = "tcp"
      port       = "22"
      source_ips = var.ssh_source_ips
    }
  }
}

resource "hcloud_server" "app" {
  name         = local.name
  server_type  = "cx23"
  image        = "ubuntu-24.04"
  location     = var.location
  backups      = true
  firewall_ids = [hcloud_firewall.server.id]
  ssh_keys     = [hcloud_ssh_key.operator.id, hcloud_ssh_key.deploy.id]

  labels = {
    environment = var.environment
    managed-by  = "terraform"
    project     = var.project_name
  }
}

# Hetzner Production Terraform

This stack creates one CX23 in `fsn1` (or `nbg1`) from Ubuntu 24.04 LTS with Hetzner backups enabled. Its firewall permits TCP ports 80 and 443 only from Cloudflare's published proxy CIDRs. Port 22 is created only when `ssh_source_ips` is non-empty.

1. Copy `terraform.tfvars.example` to untracked `terraform.tfvars` and replace the SSH public key and SSH CIDRs.
2. Export a Hetzner API token: `export HCLOUD_TOKEN=...`.
3. Run `terraform init`, then `terraform plan -var-file=terraform.tfvars`.

No API token or other secret belongs in Terraform configuration or tfvars. This environment uses local state by default; configure a remote backend before applying it for shared production operation.

The default `cloudflare_proxy_cidrs` values are Cloudflare's published ranges. Update them whenever Cloudflare changes its list before applying the firewall.

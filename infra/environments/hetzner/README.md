# Hetzner Production Terraform

This stack creates one CX23 in `fsn1` (or `nbg1`) from Ubuntu 24.04 LTS with Hetzner backups enabled. A Cloudflare Tunnel carries public traffic to the private Docker network, so the firewall has no public web ports. Port 22 is created only when `ssh_source_ips` is non-empty.

1. Copy `terraform.tfvars.example` to untracked `terraform.tfvars` and replace the SSH public key and SSH CIDRs.
2. Copy `backend.hcl.example` to untracked `backend.hcl` and configure the
   versioned, encrypted S3 state bucket created by `infra/bootstrap`.
3. Export a Hetzner API token: `export HCLOUD_TOKEN=...`.
4. Run `terraform init -backend-config=backend.hcl`, then
   `terraform plan -var-file=terraform.tfvars`.

No API token or other secret belongs in Terraform configuration or tfvars. The
production Hetzner state was migrated to this backend on 2026-08-12. Do not
reintroduce a local backend or use an old local state file. Recover a prior
version through the versioned S3 backend and a reviewed state-recovery procedure
if restoration is ever required.

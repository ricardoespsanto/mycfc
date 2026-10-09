# #318 simple production cutover and retirement

Status: implementation plan for review. It does not authorize any live action.

## Intended end state

The club keeps the manual, board-led privacy-rights process. The former automated privacy worker, activation ceremony, exchange, receipt system, and related IAM, logging, storage, and KMS infrastructure are retired. They are not reintroduced into the normal production Terraform configuration as a permanent system.

## Why the current all-at-once plan is blocked

The [23 September 2026 production preview](https://github.com/ricardoespsanto/mycfc/actions/runs/35872168119) proposes 63 standalone privacy-resource deletions plus an ECR lifecycle-policy replacement. The active application still reads `/mycfc/production/app-secrets`, while the merged configuration would deny it before the v2 application is running. The activation-audit bucket contains 153 object versions, and the other two privacy stores have Object Lock configuration. An all-at-once apply could fail partway through after removing protections. The normal protected apply correctly rejects this plan.

## One-time cutover, without reviving old automation

1. Use a dedicated, protected, narrowly targeted Terraform workflow to create only `/mycfc/production/app-runtime-secrets-v2` and its current version. Keep the existing two Terraform `moved` declarations for the legacy secret and its version, allowing only their exact state-address moves with no AWS update. Check the v2 secret's exact key-name contract without printing values. Do not change host IAM or privacy resources. Require a reviewed saved plan, exact action/address allowlist, semantic fingerprint, and separate production approval.
2. In a second independently approved protected phase, update only the existing host policy to grant `GetSecretValue` on both the old and v2 secrets, without denying the old one. Re-plan and review the now-known exact secret ARN and complete IAM JSON. Neither phase may delete, replace, import, forget, or change an unrelated live resource.
3. Verify both secret paths without displaying secret values, then deploy the signed #318 application and forward-only migration under their own release approval. Check health, runtime configuration, Polar credential continuity, restart, and host receipt. Keep old-secret read access only for a bounded rollback/observation window.
4. After the new image is stable, contract host IAM to v2-only in a separate reviewed change. From then on, rollback requires a v2-capable image. The legacy secret remains tracked through its explicit Terraform state move rather than being forgotten.

The fixed-target workflow is an exceptional bridge, not the normal release path. It must leave the normal full-root apply policy unchanged. A read-only full plan after each phase should show the expected unresolved retirement set and no new unrelated drift; a full plan is not an instruction to apply the deletions.

## Actual retirement

Inventory and disable the old host units and live capabilities first. Revoke role trusts and policies while CloudTrail still records activity; account for outstanding one-hour role sessions before deleting principals. Then retire non-data automation through a separate exact-address, protected maintenance procedure. Do not use `terraform state rm` to hide live infrastructure.

The three privacy buckets and KMS keys need a separate disposition decision, not permanent preservation. Classify the audit versions and retention/lock constraints, then obtain explicit per-store destructive approval if they can be purged. Delete exact versions and markers only when allowed, verify empty/unlocked buckets before removal, and retire KMS keys last. This sequencing protects evidence during cleanup; it does not keep the complex privacy system as an end state.

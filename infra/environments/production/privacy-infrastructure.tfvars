# Durable #274 activation posture. The identity stays inert until credentials,
# evidence, dual approval, host flags and the worker service are enabled.
privacy_worker_infrastructure_enabled        = true
privacy_worker_s3_deletion_enabled           = true
privacy_worker_metadata_rewrite_enabled      = true
privacy_worker_ledger_broker_invoke_enabled  = true
privacy_worker_ledger_broker_function_arn    = "arn:aws:lambda:eu-west-1:334960985019:function:mycfc-production-privacy-ledger-broker"
privacy_worker_monitoring_enabled            = true
operations_observer_enabled                  = true
operations_observer_github_oidc_provider_arn = "arn:aws:iam::334960985019:oidc-provider/token.actions.githubusercontent.com"
legacy_media_purge_identity_enabled          = false
legacy_media_purge_deletion_enabled          = false
legacy_media_purge_write_fence_enabled       = false
legacy_media_purge_permission_expires_at     = null

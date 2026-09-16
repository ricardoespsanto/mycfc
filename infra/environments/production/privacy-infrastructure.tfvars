# Durable #274 first stage. All protected workflow plans explicitly load this
# file after secret auto.tfvars. Change these gates only through reviewed source.
privacy_worker_infrastructure_enabled       = true
privacy_worker_s3_deletion_enabled          = false
privacy_worker_metadata_rewrite_enabled     = false
privacy_worker_ledger_broker_invoke_enabled = false
privacy_worker_ledger_broker_function_arn   = null
privacy_worker_monitoring_enabled           = false
legacy_media_purge_identity_enabled         = false
legacy_media_purge_deletion_enabled         = false
legacy_media_purge_write_fence_enabled      = false
legacy_media_purge_permission_expires_at    = null
